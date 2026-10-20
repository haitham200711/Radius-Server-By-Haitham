package speedutil

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseRateTokenToKbps converts tokens like "512k", "2M", "10M", "1048576" into kbps
func ParseRateTokenToKbps(s string) (float64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty rate")
	}

	multiplier := 1.0 // default kbps
	if strings.HasSuffix(s, "k") {
		multiplier = 1.0
		s = strings.TrimSuffix(s, "k")
	} else if strings.HasSuffix(s, "m") {
		multiplier = 1024.0
		s = strings.TrimSuffix(s, "m")
	} else if strings.HasSuffix(s, "g") {
		multiplier = 1024.0 * 1024.0
		s = strings.TrimSuffix(s, "g")
	} else {
		val, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, err
		}
		if val >= 100000 {
			return val / 1024.0, nil
		}
		return val, nil
	}

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return val * multiplier, nil
}

// FormatKbps converts kbps back to a clean MikroTik friendly string ("512k", "1M", "2M", "1536k")
func FormatKbps(kbps float64) string {
	if kbps <= 0 {
		return "1M"
	}
	ikbps := int64(math.Round(kbps))
	if ikbps >= 1024 && ikbps%1024 == 0 {
		return fmt.Sprintf("%dM", ikbps/1024)
	}
	// If near an integer MB (within float rounding)
	if ikbps >= 1024 {
		mb := float64(ikbps) / 1024.0
		if math.Abs(mb-math.Round(mb)) < 0.001 {
			return fmt.Sprintf("%dM", int64(math.Round(mb)))
		}
	}
	return fmt.Sprintf("%dk", ikbps)
}

// BoostSingleRate applies a multiplier or addition to a single rate string (e.g. "512k" or "2M")
func BoostSingleRate(rateStr string, boostType string, boostVal float64) string {
	kbps, err := ParseRateTokenToKbps(rateStr)
	if err != nil {
		return rateStr
	}

	var newKbps float64
	if boostType == "add_fixed" {
		// boostVal in Mbps (e.g. +5 means +5120 kbps)
		newKbps = kbps + (boostVal * 1024.0)
	} else {
		// multiply by boostVal (e.g. 2.0x, 1.5x)
		if boostVal <= 0 {
			boostVal = 1.0
		}
		newKbps = kbps * boostVal
	}

	return FormatKbps(newKbps)
}

// BoostRateLimit takes a full rate limit like "512k/1M" or "512k/1M 1M/2M ..." and boosts it
func BoostRateLimit(fullRate string, boostType string, boostVal float64) string {
	fullRate = strings.TrimSpace(fullRate)
	if fullRate == "" {
		return ""
	}

	tokens := strings.Fields(fullRate)
	for i, token := range tokens {
		if strings.Contains(token, "/") {
			parts := strings.Split(token, "/")
			if len(parts) == 2 {
				rx := BoostSingleRate(parts[0], boostType, boostVal)
				tx := BoostSingleRate(parts[1], boostType, boostVal)
				tokens[i] = rx + "/" + tx
			}
		} else if i == 0 {
			tokens[i] = BoostSingleRate(token, boostType, boostVal)
		}
	}

	return strings.Join(tokens, " ")
}
