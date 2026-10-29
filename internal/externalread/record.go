package externalread

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"contractaudit/internal/auditext"
)

const (
	configuredBandwidthSourceName = "configuredBandwidth"
	configuredBandwidthFieldName  = "configured_bandwidth"
	configuredBandwidthSemantic   = "configured_value"
)

// noIntervalFrom/noIntervalUntil form a fixed, empty sentinel interval for
// observations without a source-declared validity interval. It cannot cover a
// real business instant, so it never claims validity the source did not state.
var (
	noIntervalFrom  = time.Unix(0, 0).UTC()
	noIntervalUntil = time.Unix(0, 1).UTC()
)

var quantityPattern = regexp.MustCompile(`^\d+(\.\d+)?$`)

type rawRecord struct {
	ID                    json.RawMessage `json:"id"`
	Version               json.RawMessage `json:"version"`
	UpdatedAt             json.RawMessage `json:"updatedAt"`
	EffectiveFrom         json.RawMessage `json:"effectiveFrom"`
	EffectiveUntil        json.RawMessage `json:"effectiveUntil"`
	ServiceCharacteristic json.RawMessage `json:"serviceCharacteristic"`
}

type rawCharacteristic struct {
	Name  json.RawMessage `json:"name"`
	Value json.RawMessage `json:"value"`
	Unit  json.RawMessage `json:"unit"`
}

// recordData is the normalized parse result. Flags record which source fields
// were absent or invalid so Read can pick the contract-mandated reason code.
type recordData struct {
	recordID      string
	recordIDValid bool

	recordVersion  string
	hasVersion     bool
	versionInvalid bool

	updatedAt        time.Time
	hasUpdatedAt     bool
	updatedAtInvalid bool

	effectiveFrom   time.Time
	effectiveUntil  time.Time
	hasInterval     bool
	intervalInvalid bool

	field    auditext.Field
	hasField bool
}

// parseRecord decodes one synthetic OSS record. It reports false only when the
// payload is not a JSON object at all; field-level defects are returned as
// flags for the caller to map to the contract reason codes.
func parseRecord(payload []byte) (*recordData, bool) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, false
	}
	var raw rawRecord
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, false
	}

	data := &recordData{}
	parseRecordID(data, raw.ID)
	parseRecordVersion(data, raw.Version)
	parseRecordUpdatedAt(data, raw.UpdatedAt)
	parseRecordInterval(data, raw.EffectiveFrom, raw.EffectiveUntil)
	data.parseConfiguredBandwidth(raw.ServiceCharacteristic)
	return data, true
}

func parseRecordID(data *recordData, raw json.RawMessage) {
	if id, present, valid := jsonString(raw); present && valid && id != "" && auditext.ValidateID(id) == nil {
		data.recordID = id
		data.recordIDValid = true
	}
}

func parseRecordVersion(data *recordData, raw json.RawMessage) {
	if version, present, valid := jsonString(raw); !present {
		return
	} else if !valid || len(version) > 128 {
		data.versionInvalid = true
	} else if version != "" {
		data.recordVersion = version
		data.hasVersion = true
	}
}

func parseRecordUpdatedAt(data *recordData, raw json.RawMessage) {
	updatedAt, present, valid := jsonString(raw)
	if !present {
		return
	}
	if !valid {
		data.updatedAtInvalid = true
		return
	}
	parsed, err := time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		data.updatedAtInvalid = true
		return
	}
	data.updatedAt = parsed
	data.hasUpdatedAt = true
}

func parseRecordInterval(data *recordData, rawFrom, rawUntil json.RawMessage) {
	from, fromPresent, fromValid := jsonString(rawFrom)
	until, untilPresent, untilValid := jsonString(rawUntil)
	if !fromPresent || !fromValid || !untilPresent || !untilValid {
		data.intervalInvalid = true
		return
	}
	parsedFrom, fromErr := time.Parse(time.RFC3339, from)
	parsedUntil, untilErr := time.Parse(time.RFC3339, until)
	if fromErr != nil || untilErr != nil || !parsedFrom.Before(parsedUntil) {
		data.intervalInvalid = true
		return
	}
	data.effectiveFrom = parsedFrom
	data.effectiveUntil = parsedUntil
	data.hasInterval = true
}

func (d *recordData) parseConfiguredBandwidth(rawCharacteristics json.RawMessage) {
	if len(rawCharacteristics) == 0 || isJSONNull(rawCharacteristics) {
		return
	}
	var characteristics []rawCharacteristic
	if err := json.Unmarshal(rawCharacteristics, &characteristics); err != nil {
		return
	}
	for index, characteristic := range characteristics {
		field, matches, valid := configuredBandwidthCharacteristic(index, characteristic)
		if !matches {
			continue
		}
		if !valid {
			// The configured characteristic exists but is unusable; do not
			// fall through to a later duplicate entry.
			return
		}
		d.field = field
		d.hasField = true
		return
	}
}

func configuredBandwidthCharacteristic(index int, characteristic rawCharacteristic) (auditext.Field, bool, bool) {
	name, present, valid := jsonString(characteristic.Name)
	if !present || !valid || name != configuredBandwidthSourceName {
		return auditext.Field{}, false, false
	}
	value, valuePresent, valueValid := jsonString(characteristic.Value)
	unit, unitPresent, unitValid := jsonString(characteristic.Unit)
	if !validConfiguredBandwidthField(value, valuePresent, valueValid, unit, unitPresent, unitValid) {
		return auditext.Field{}, true, false
	}
	return auditext.Field{
		Name:          configuredBandwidthFieldName,
		Value:         value,
		Unit:          unit,
		SourcePointer: fmt.Sprintf("/serviceCharacteristic/%d/value", index),
		SemanticKind:  configuredBandwidthSemantic,
	}, true, true
}

func validConfiguredBandwidthField(value string, valuePresent, valueValid bool, unit string, unitPresent, unitValid bool) bool {
	return valuePresent && valueValid && unitPresent && unitValid && validConfiguredQuantity(value) && validBandwidthUnit(unit)
}

func jsonString(raw json.RawMessage) (value string, present bool, valid bool) {
	if len(raw) == 0 || isJSONNull(raw) {
		return "", false, true
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, false
	}
	return value, true, true
}

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func validConfiguredQuantity(value string) bool {
	return value != "" && len(value) <= 64 && quantityPattern.MatchString(value)
}

func validBandwidthUnit(unit string) bool {
	return unit == "Mbit/s" || unit == "Gbit/s"
}
