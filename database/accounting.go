package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/haitham200711/Radius-Server-By-Haitham/pkg/radius"
)

var (
	ErrDuplicateStopPacket = errors.New("duplicate stop packet ignored")
)

// AccountingRecord represents a normalized incoming RADIUS Accounting request
type AccountingRecord struct {
	Username         string
	NASIP            string
	SessionID        string
	UniqueSessionID  string
	StatusType       int
	InputOctets      int64
	OutputOctets     int64
	InputGigawords   uint32
	OutputGigawords  uint32
	SessionTime      uint32
	TerminateCause   string
	FramedIP         string
	CallingStationID string
	CalledStationID  string
	EventTimestamp   time.Time
}

// CalculateNormalizedCounters computes 64-bit byte totals from 32-bit octets and gigawords
func CalculateNormalizedCounters(octets int64, gigawords uint32) uint64 {
	return (uint64(gigawords) * 4294967296) + uint64(octets)
}

// ProcessAccountingRecord processes a RADIUS accounting request idempotently in MySQL
func (d *DB) ProcessAccountingRecord(rec *AccountingRecord) error {
	if rec.NASIP == "" || (rec.SessionID == "" && rec.StatusType != radius.AcctStatusAccountingOn && rec.StatusType != radius.AcctStatusAccountingOff) {
		return fmt.Errorf("invalid accounting record: NAS IP and Session ID are required")
	}

	normInput := CalculateNormalizedCounters(rec.InputOctets, rec.InputGigawords)
	normOutput := CalculateNormalizedCounters(rec.OutputOctets, rec.OutputGigawords)

	switch rec.StatusType {
	case radius.AcctStatusStart:
		// 1. Accounting-Start: Idempotent session initialization
		// If session already exists, update start time only if null
		_, err := d.Exec(`INSERT INTO radacct (
			acctsessionid, acctuniqueid, username, realm, nasipaddress,
			acctstarttime, acctupdatetime, acctstoptime, acctsessiontime,
			acctinputoctets, acctoutputoctets, acctinputgigawords, acctoutputgigawords,
			normalized_input_bytes, normalized_output_bytes,
			calledstationid, callingstationid, acctterminatecause, framedipaddress
		) VALUES (?, ?, ?, '', ?, NOW(), NOW(), NULL, 0, 0, 0, 0, 0, 0, 0, ?, ?, '', ?)
		ON DUPLICATE KEY UPDATE
			acctupdatetime = NOW(),
			acctstarttime = COALESCE(acctstarttime, NOW()),
			framedipaddress = VALUES(framedipaddress)`,
			rec.SessionID, rec.UniqueSessionID, rec.Username, rec.NASIP,
			rec.CalledStationID, rec.CallingStationID, rec.FramedIP)
		return err

	case radius.AcctStatusInterimUpdate:
		// 2. Accounting-Interim-Update: In-place counter update (NEVER duplicate sums)
		res, err := d.Exec(`UPDATE radacct SET
			acctupdatetime = NOW(),
			acctsessiontime = ?,
			acctinputoctets = ?,
			acctoutputoctets = ?,
			acctinputgigawords = ?,
			acctoutputgigawords = ?,
			normalized_input_bytes = ?,
			normalized_output_bytes = ?,
			framedipaddress = COALESCE(NULLIF(?, ''), framedipaddress)
		WHERE acctsessionid = ? AND nasipaddress = ? AND acctstoptime IS NULL`,
			rec.SessionTime, rec.InputOctets, rec.OutputOctets, rec.InputGigawords, rec.OutputGigawords,
			normInput, normOutput, rec.FramedIP, rec.SessionID, rec.NASIP)
		if err != nil {
			return fmt.Errorf("failed to update interim accounting: %w", err)
		}

		rowsAff, _ := res.RowsAffected()
		if rowsAff == 0 {
			// Start packet was dropped or delayed over UDP; insert active session
			startTime := time.Now().Add(-time.Duration(rec.SessionTime) * time.Second)
			_, err = d.Exec(`INSERT INTO radacct (
				acctsessionid, acctuniqueid, username, realm, nasipaddress,
				acctstarttime, acctupdatetime, acctstoptime, acctsessiontime,
				acctinputoctets, acctoutputoctets, acctinputgigawords, acctoutputgigawords,
				normalized_input_bytes, normalized_output_bytes,
				calledstationid, callingstationid, acctterminatecause, framedipaddress
			) VALUES (?, ?, ?, '', ?, ?, NOW(), NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?)
			ON DUPLICATE KEY UPDATE
				acctupdatetime = NOW(),
				acctsessiontime = VALUES(acctsessiontime),
				acctinputoctets = VALUES(acctinputoctets),
				acctoutputoctets = VALUES(acctoutputoctets),
				acctinputgigawords = VALUES(acctinputgigawords),
				acctoutputgigawords = VALUES(acctoutputgigawords),
				normalized_input_bytes = VALUES(normalized_input_bytes),
				normalized_output_bytes = VALUES(normalized_output_bytes)`,
				rec.SessionID, rec.UniqueSessionID, rec.Username, rec.NASIP,
				startTime, rec.SessionTime, rec.InputOctets, rec.OutputOctets, rec.InputGigawords, rec.OutputGigawords,
				normInput, normOutput, rec.CalledStationID, rec.CallingStationID, rec.FramedIP)
			return err
		}
		return nil

	case radius.AcctStatusStop:
		// 3. Accounting-Stop: Finalize session and close cleanly
		// Check if already stopped (idempotent duplicate Stop handling)
		var stopTime sql.NullTime
		err := d.QueryRow(`SELECT acctstoptime FROM radacct 
			WHERE acctsessionid = ? AND nasipaddress = ? ORDER BY radacctid DESC LIMIT 1`,
			rec.SessionID, rec.NASIP).Scan(&stopTime)
		if err == nil && stopTime.Valid {
			// Already closed cleanly; ignore duplicate Stop without inflating usage
			return nil
		}

		res, err := d.Exec(`UPDATE radacct SET
			acctstoptime = NOW(),
			acctupdatetime = NOW(),
			acctsessiontime = ?,
			acctinputoctets = ?,
			acctoutputoctets = ?,
			acctinputgigawords = ?,
			acctoutputgigawords = ?,
			normalized_input_bytes = ?,
			normalized_output_bytes = ?,
			acctterminatecause = ?,
			disconnect_status = 'completed'
		WHERE acctsessionid = ? AND nasipaddress = ? AND acctstoptime IS NULL`,
			rec.SessionTime, rec.InputOctets, rec.OutputOctets, rec.InputGigawords, rec.OutputGigawords,
			normInput, normOutput, rec.TerminateCause, rec.SessionID, rec.NASIP)
		if err != nil {
			return fmt.Errorf("failed to finalize stop accounting: %w", err)
		}

		rowsAff, _ := res.RowsAffected()
		if rowsAff == 0 {
			// Session was never started (packet loss on Start/Interim); record stopped session directly
			startTime := time.Now().Add(-time.Duration(rec.SessionTime) * time.Second)
			_, err = d.Exec(`INSERT INTO radacct (
				acctsessionid, acctuniqueid, username, realm, nasipaddress,
				acctstarttime, acctupdatetime, acctstoptime, acctsessiontime,
				acctinputoctets, acctoutputoctets, acctinputgigawords, acctoutputgigawords,
				normalized_input_bytes, normalized_output_bytes,
				calledstationid, callingstationid, acctterminatecause, framedipaddress
			) VALUES (?, ?, ?, '', ?, ?, NOW(), NOW(), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				rec.SessionID, rec.UniqueSessionID, rec.Username, rec.NASIP,
				startTime, rec.SessionTime, rec.InputOctets, rec.OutputOctets, rec.InputGigawords, rec.OutputGigawords,
				normInput, normOutput, rec.CalledStationID, rec.CallingStationID, rec.TerminateCause, rec.FramedIP)
			return err
		}
		return nil

	case radius.AcctStatusAccountingOn, radius.AcctStatusAccountingOff:
		// 4. NAS Reboot / Reset: Mark all previously active sessions on this NAS as terminated
		_, err := d.Exec(`UPDATE radacct SET
			acctstoptime = NOW(),
			acctterminatecause = 'NAS-Reboot',
			disconnect_status = 'nas_reboot'
		WHERE nasipaddress = ? AND acctstoptime IS NULL`, rec.NASIP)
		return err

	default:
		return nil
	}
}

// ProcessAccountingPacket decodes an RFC 2866 RADIUS Accounting-Request packet,
// processes it through the database, and returns the appropriate Accounting-Response packet.
func (d *DB) ProcessAccountingPacket(pkt *radius.Packet, nasIP string, secret []byte) (*radius.Packet, error) {
	if pkt == nil {
		return nil, errors.New("nil packet")
	}
	if pkt.Code != radius.CodeAccountingRequest {
		return nil, fmt.Errorf("invalid packet code: %d (expected Accounting-Request)", pkt.Code)
	}

	// Verify request authenticator if secret is provided
	if len(secret) > 0 && !pkt.VerifyAccountingRequest(secret) {
		return nil, errors.New("invalid accounting request authenticator")
	}

	rec := &AccountingRecord{
		NASIP:            nasIP,
		Username:         pkt.GetString(radius.AttrUserName),
		SessionID:        pkt.GetString(radius.AttrAcctSessionID),
		UniqueSessionID:  pkt.GetString(radius.AttrAcctMultiSessionID),
		StatusType:       int(pkt.GetUint32(radius.AttrAcctStatusType)),
		InputOctets:      int64(pkt.GetUint32(radius.AttrAcctInputOctets)),
		OutputOctets:     int64(pkt.GetUint32(radius.AttrAcctOutputOctets)),
		InputGigawords:   pkt.GetUint32(radius.AttrAcctInputGigawords),
		OutputGigawords:  pkt.GetUint32(radius.AttrAcctOutputGigawords),
		SessionTime:      pkt.GetUint32(radius.AttrAcctSessionTime),
		CallingStationID: pkt.GetString(radius.AttrCallingStationID),
		CalledStationID:  pkt.GetString(radius.AttrCalledStationID),
	}

	if rec.UniqueSessionID == "" {
		rec.UniqueSessionID = rec.SessionID
	}

	if tc := pkt.GetUint32(radius.AttrAcctTerminateCause); tc != 0 {
		rec.TerminateCause = fmt.Sprintf("Cause-%d", tc)
	}
	if ip := pkt.GetIP(radius.AttrFramedIPAddress); ip != nil {
		rec.FramedIP = ip.String()
	}

	if err := d.ProcessAccountingRecord(rec); err != nil {
		return nil, fmt.Errorf("error processing accounting record: %w", err)
	}

	// Create Accounting-Response packet
	resp := radius.NewPacket(radius.CodeAccountingResponse, pkt.Identifier, secret)
	copy(resp.Authenticator[:], pkt.Authenticator[:])
	return resp, nil
}
