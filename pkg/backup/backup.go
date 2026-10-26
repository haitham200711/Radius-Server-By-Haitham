package backup

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/config"
	"github.com/haitham200711/Radius-Server-By-Haitham/database"
	"github.com/haitham200711/Radius-Server-By-Haitham/models"
	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/whatsapp"
)

var (
	backupMu  sync.Mutex
	BackupDir = "./backups"
)

// SetBackupDir customizes the backup directory path
func SetBackupDir(dir string) {
	if dir != "" {
		BackupDir = dir
	}
}

// ExecuteBackup runs a complete database backup, compresses with gzip,
// enforces the retention count policy, and sends via WhatsApp if configured.
func ExecuteBackup(db *database.DB, cfg *config.Config, triggerSource string) (*models.BackupRecord, error) {
	backupMu.Lock()
	defer backupMu.Unlock()

	if err := os.MkdirAll(BackupDir, 0755); err != nil {
		return nil, fmt.Errorf("فشل إنشاء مجلد النسخ الاحتياطية: %w", err)
	}

	settings, err := db.GetBackupSettings()
	if err != nil {
		settings = &models.BackupSettings{
			Enabled:        true,
			IntervalHours:  6,
			RetentionCount: 10,
		}
	}

	timestamp := time.Now().Format("2006-01-02-150405")
	filename := fmt.Sprintf("radius-backup-%s.sql.gz", timestamp)
	targetPath := filepath.Join(BackupDir, filename)

	log.Printf("[BACKUP] Starting database backup -> %s (Trigger: %s)...", targetPath, triggerSource)

	dbUser, dbPass, dbHost, dbPort, dbName := parseMySQLDSN(cfg.DBDSN)

	// Attempt 1: Fast native mariadb-dump / mysqldump utility
	dumpErr := dumpWithCLI(dbUser, dbPass, dbHost, dbPort, dbName, targetPath)
	if dumpErr != nil {
		log.Printf("[BACKUP] CLI dump failed (%v), falling back to Go pure SQL exporter...", dumpErr)
		// Fallback: Pure Go database dumper
		if err := dumpWithGo(db, targetPath); err != nil {
			_ = db.UpdateBackupRunStatus("failed", err.Error())
			return nil, fmt.Errorf("فشل إنشاء النسخة الاحتياطية: %w", err)
		}
	}

	// Verify file size
	fi, err := os.Stat(targetPath)
	if err != nil || fi.Size() == 0 {
		_ = os.Remove(targetPath)
		_ = db.UpdateBackupRunStatus("failed", "ملف النسخة فارغ أو تعذر إنشاؤه")
		return nil, fmt.Errorf("ملف النسخة الاحتياطية فارغ أو تعذر حفظه")
	}

	rec := &models.BackupRecord{
		Filename:     filename,
		FilePath:     targetPath,
		SizeBytes:    fi.Size(),
		CreatedAt:    time.Now(),
		Status:       "success",
		WhatsAppSent: false,
	}

	id, err := db.CreateBackupRecord(rec)
	if err != nil {
		log.Printf("[BACKUP] Warning: failed to save record in database: %v", err)
	} else {
		rec.ID = id
	}

	// Enforce Retention Policy: Keep only recent N copies
	enforceRetention(db, settings.RetentionCount)

	// WhatsApp Dispatch
	if settings.SendWhatsApp && strings.TrimSpace(settings.WhatsAppPhone) != "" {
		go sendBackupViaWhatsApp(db, rec, settings.WhatsAppPhone, triggerSource)
	}

	_ = db.UpdateBackupRunStatus("success", "")
	log.Printf("[BACKUP] Backup completed successfully (%s, %d bytes)", filename, fi.Size())

	return rec, nil
}

// dumpWithCLI executes mariadb-dump / mysqldump and pipes through gzip securely without leaking credentials in ps
func dumpWithCLI(user, pass, host, port, dbname, targetPath string) error {
	dumpBin := ""
	for _, bin := range []string{"mariadb-dump", "mysqldump", "/usr/bin/mariadb-dump", "/usr/bin/mysqldump"} {
		if p, err := exec.LookPath(bin); err == nil {
			dumpBin = p
			break
		}
	}

	if dumpBin == "" {
		return fmt.Errorf("mariadb-dump or mysqldump not found in PATH")
	}

	// Create a secure temporary MySQL options file (0600) to avoid exposing credentials in process table
	optFile, err := os.CreateTemp("", "radius_dump_*.cnf")
	if err != nil {
		return fmt.Errorf("failed to create temporary options file: %w", err)
	}
	defer os.Remove(optFile.Name())
	_ = optFile.Chmod(0600)

	optContent := fmt.Sprintf("[client]\nuser=%s\npassword=%s\nhost=%s\nport=%s\n", user, pass, host, port)
	if _, err := optFile.WriteString(optContent); err != nil {
		_ = optFile.Close()
		return fmt.Errorf("failed to write options file: %w", err)
	}
	_ = optFile.Close()

	outFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	gzWriter := gzip.NewWriter(outFile)
	defer gzWriter.Close()

	args := []string{
		fmt.Sprintf("--defaults-extra-file=%s", optFile.Name()),
		"--single-transaction",
		"--quick",
		"--routines",
		"--triggers",
		dbname,
	}

	cmd := exec.Command(dumpBin, args...)
	cmd.Stdout = gzWriter
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderr.String())
	}

	return nil
}

// dumpWithGo performs a pure Go schema & data dump as robust fallback
func dumpWithGo(db *database.DB, targetPath string) error {
	outFile, err := os.Create(targetPath)
	if err != nil {
		return err
	}
	defer outFile.Close()

	gzWriter := gzip.NewWriter(outFile)
	defer gzWriter.Close()

	header := fmt.Sprintf("-- HAITHAM RADIUS ENTERPRISE DATABASE DUMP\n-- Date: %s\n\nSET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS = 0;\n\n", time.Now().Format("2006-01-02 15:04:05"))
	if _, err := gzWriter.Write([]byte(header)); err != nil {
		return err
	}

	rows, err := db.Query("SHOW FULL TABLES WHERE Table_type = 'BASE TABLE'")
	if err != nil {
		return err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table, tableType string
		if err := rows.Scan(&table, &tableType); err == nil {
			tables = append(tables, table)
		}
	}

	for _, tbl := range tables {
		// Table Schema
		var tblName, createSQL string
		if err := db.QueryRow(fmt.Sprintf("SHOW CREATE TABLE `%s`", tbl)).Scan(&tblName, &createSQL); err == nil {
			stmt := fmt.Sprintf("DROP TABLE IF EXISTS `%s`;\n%s;\n\n", tbl, createSQL)
			_, _ = gzWriter.Write([]byte(stmt))
		}

		// Table Rows
		dataRows, err := db.Query(fmt.Sprintf("SELECT * FROM `%s`", tbl))
		if err != nil {
			continue
		}

		cols, _ := dataRows.Columns()
		for dataRows.Next() {
			vals := make([]interface{}, len(cols))
			valPtrs := make([]interface{}, len(cols))
			for i := range cols {
				valPtrs[i] = &vals[i]
			}
			if err := dataRows.Scan(valPtrs...); err != nil {
				continue
			}

			var rowVals []string
			for _, v := range vals {
				if v == nil {
					rowVals = append(rowVals, "NULL")
				} else {
					switch val := v.(type) {
					case []byte:
						escaped := strings.ReplaceAll(string(val), "'", "''")
						escaped = strings.ReplaceAll(escaped, "\\", "\\\\")
						rowVals = append(rowVals, fmt.Sprintf("'%s'", escaped))
					default:
						escaped := strings.ReplaceAll(fmt.Sprintf("%v", val), "'", "''")
						escaped = strings.ReplaceAll(escaped, "\\", "\\\\")
						rowVals = append(rowVals, fmt.Sprintf("'%s'", escaped))
					}
				}
			}
			ins := fmt.Sprintf("INSERT INTO `%s` VALUES (%s);\n", tbl, strings.Join(rowVals, ", "))
			_, _ = gzWriter.Write([]byte(ins))
		}
		dataRows.Close()
		_, _ = gzWriter.Write([]byte("\n"))
	}

	_, _ = gzWriter.Write([]byte("SET FOREIGN_KEY_CHECKS = 1;\n"))
	return nil
}

// enforceRetention deletes backups older than retentionCount
func enforceRetention(db *database.DB, retentionCount int) {
	if retentionCount <= 0 {
		retentionCount = 10
	}

	list, err := db.ListBackups()
	if err != nil {
		return
	}

	if len(list) > retentionCount {
		for i := retentionCount; i < len(list); i++ {
			old := list[i]
			log.Printf("[BACKUP-RETENTION] Pruning old backup (Retention: %d): %s", retentionCount, old.Filename)
			if old.FilePath != "" {
				_ = os.Remove(old.FilePath)
			}
			_ = db.DeleteBackupRecord(old.ID)
		}
	}
}

// sendBackupViaWhatsApp dispatches the backup file to the recipient via WhatsApp
func sendBackupViaWhatsApp(db *database.DB, rec *models.BackupRecord, phone string, triggerSource string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[BACKUP-WHATSAPP] Recovered from panic: %v", r)
		}
	}()

	fileBytes, err := os.ReadFile(rec.FilePath)
	if err != nil {
		log.Printf("[BACKUP-WHATSAPP] Failed to read backup file %s: %v", rec.FilePath, err)
		return
	}

	caption := fmt.Sprintf(`📦 *نسخة احتياطية لنظام Haitham RADIUS*
══════════════════════════
📅 *التاريخ:* %s
📁 *اسم الملف:* %s
📊 *الحجم:* %s
⚙️ *طريقة التنفيذ:* %s
══════════════════════════
🛡️ تم أخذ النسخة بنجاح وتطبيق سياسة الحفظ التلقائي.`,
		rec.CreatedAt.Format("2006-01-02 15:04:05"),
		rec.Filename,
		database.FormatBytesHuman(rec.SizeBytes),
		triggerSource,
	)

	log.Printf("[BACKUP-WHATSAPP] Sending backup archive %s to WhatsApp phone: %s", rec.Filename, phone)
	if err := whatsapp.SendWhatsAppDocument(phone, fileBytes, rec.Filename, caption); err != nil {
		log.Printf("[BACKUP-WHATSAPP] Failed to send via WhatsApp: %v", err)
	} else {
		log.Printf("[BACKUP-WHATSAPP] Backup archive %s sent successfully via WhatsApp to %s", rec.Filename, phone)
		if rec.ID > 0 {
			_ = db.MarkBackupWhatsAppSent(rec.ID, true)
		}
	}
}

// RestoreBackup restores database tables from a compressed .sql.gz or plain .sql file safely
func RestoreBackup(db *database.DB, cfg *config.Config, backupFilePath string) error {
	backupMu.Lock()
	defer backupMu.Unlock()

	cleanedPath := filepath.Clean(backupFilePath)
	f, err := os.Open(cleanedPath)
	if err != nil {
		return fmt.Errorf("تعذر فتح ملف النسخة: %w", err)
	}
	defer f.Close()

	var rawReader io.Reader = f
	if strings.HasSuffix(strings.ToLower(cleanedPath), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("فشل فك ضغط النسخة: %w", err)
		}
		defer gz.Close()
		rawReader = gz
	}

	// Protect against decompression bombs: enforce strict 2GB decompressed stream limit
	reader := io.LimitReader(rawReader, 2<<30)

	dbUser, dbPass, dbHost, dbPort, dbName := parseMySQLDSN(cfg.DBDSN)

	// Try CLI restore first using secure temporary options file (0600)
	restoreBin := ""
	for _, bin := range []string{"mariadb", "mysql", "/usr/bin/mariadb", "/usr/bin/mysql"} {
		if p, err := exec.LookPath(bin); err == nil {
			restoreBin = p
			break
		}
	}

	if restoreBin != "" {
		optFile, err := os.CreateTemp("", "radius_restore_*.cnf")
		if err != nil {
			return fmt.Errorf("failed to create temporary restore options file: %w", err)
		}
		defer os.Remove(optFile.Name())
		_ = optFile.Chmod(0600)

		optContent := fmt.Sprintf("[client]\nuser=%s\npassword=%s\nhost=%s\nport=%s\n", dbUser, dbPass, dbHost, dbPort)
		if _, err := optFile.WriteString(optContent); err != nil {
			_ = optFile.Close()
			return fmt.Errorf("failed to write restore options file: %w", err)
		}
		_ = optFile.Close()

		cmd := exec.Command(restoreBin, fmt.Sprintf("--defaults-extra-file=%s", optFile.Name()), dbName)
		cmd.Stdin = reader
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("فشل استعادة قاعدة البيانات: %w (%s)", err, stderr.String())
		}
		log.Printf("[AUDIT] Database restored successfully from backup file: %s", filepath.Base(cleanedPath))
		return nil
	}

	// Fallback pure Go restore inside transaction: read SQL content and execute
	sqlBytes, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("فشل قراءة محتوى SQL: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to start database transaction for restore: %w", err)
	}
	defer tx.Rollback()

	queries := strings.Split(string(sqlBytes), ";\n")
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" || strings.HasPrefix(q, "--") {
			continue
		}
		if _, err := tx.Exec(q); err != nil {
			log.Printf("[BACKUP-RESTORE] Warning executing query: %v", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit restored data: %w", err)
	}

	log.Printf("[AUDIT] Pure-Go fallback restore completed successfully from: %s", filepath.Base(cleanedPath))
	return nil
}

// StartBackupScheduler initializes the automated background backup daemon
func StartBackupScheduler(db *database.DB, cfg *config.Config) {
	go func() {
		time.Sleep(15 * time.Second)
		log.Println("[BACKUP-SCHEDULER] Automated backup scheduler daemon started.")

		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			settings, err := db.GetBackupSettings()
			if err != nil || !settings.Enabled {
				continue
			}

			interval := settings.IntervalHours
			if interval < 1 {
				interval = 1
			}

			shouldRun := false
			if settings.LastBackupAt == nil {
				shouldRun = true
			} else if time.Since(*settings.LastBackupAt) >= time.Duration(interval)*time.Hour {
				shouldRun = true
			}

			if shouldRun {
				log.Printf("[BACKUP-SCHEDULER] Interval (%d hours) reached. Executing scheduled backup...", interval)
				_, err := ExecuteBackup(db, cfg, "تلقائي (جدول زمني)")
				if err != nil {
					log.Printf("[BACKUP-SCHEDULER] Scheduled backup error: %v", err)
				} else {
					log.Println("[BACKUP-SCHEDULER] Scheduled backup finished successfully.")
				}
			}
		}
	}()
}

func parseMySQLDSN(dsn string) (user, pass, host, port, dbname string) {
	user = "radius"
	pass = ""
	host = "127.0.0.1"
	port = "3306"
	dbname = "radiusdb"

	atIdx := strings.LastIndex(dsn, "@")
	if atIdx != -1 {
		userPass := dsn[:atIdx]
		upParts := strings.SplitN(userPass, ":", 2)
		if len(upParts) == 2 {
			user = upParts[0]
			pass = upParts[1]
		}

		rest := dsn[atIdx+1:]
		if tcpIdx := strings.Index(rest, "tcp("); tcpIdx != -1 {
			closeParen := strings.Index(rest[tcpIdx:], ")")
			if closeParen != -1 {
				hp := rest[tcpIdx+4 : tcpIdx+closeParen]
				h, p, err := net.SplitHostPort(hp)
				if err == nil {
					host = h
					port = p
				} else {
					host = hp
				}
				rest = rest[tcpIdx+closeParen+1:]
			}
		}

		if slashIdx := strings.Index(rest, "/"); slashIdx != -1 {
			dbPart := rest[slashIdx+1:]
			if qIdx := strings.Index(dbPart, "?"); qIdx != -1 {
				dbname = dbPart[:qIdx]
			} else {
				dbname = dbPart
			}
		}
	}
	return
}
