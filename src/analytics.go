package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

type ampPayload struct {
	APIKey string     `json:"api_key"`
	Events []AmpEvent `json:"events"`
}

// AmpEvent the analytic event
type AmpEvent struct {
	UserID             string                 `json:"user_id,omitempty"`
	DeviceID           string                 `json:"device_id,omitempty"`
	EventType          string                 `json:"event_type,omitempty"`
	EventID            int                    `json:"event_id,omitempty"`
	SessionID          int64                  `json:"session_id,omitempty"`
	InsertID           string                 `json:"insert_id,omitempty"` // for dedupe
	EpochTime          int64                  `json:"time,omitempty"`
	EventProperties    map[string]interface{} `json:"event_properties,omitempty"`
	UserProperties     map[string]interface{} `json:"user_properties,omitempty"`
	AppVersion         string                 `json:"app_version,omitempty"`
	Platform           string                 `json:"platform,omitempty"`
	OSName             string                 `json:"os_name,omitempty"`
	OSVersion          string                 `json:"os_version,omitempty"`
	DeviceBrand        string                 `json:"device_brand,omitempty"`
	DeviceManufacturer string                 `json:"device_manufacturer,omitempty"`
	DeviceModel        string                 `json:"device_model,omitempty"`
	DeviceType         string                 `json:"device_type,omitempty"`
	Carrier            string                 `json:"carrier,omitempty"`
	Country            string                 `json:"country,omitempty"`
	Region             string                 `json:"region,omitempty"`
	City               string                 `json:"city,omitempty"`
	DMA                string                 `json:"dma,omitempty"`
	Language           string                 `json:"language,omitempty"`
	Revenue            float64                `json:"revenue,omitempty"`
	RevenueType        string                 `json:"revenueType,omitempty"`
	Latitude           float64                `json:"location_lat,omitempty"`
	Longitude          float64                `json:"location_lng,omitempty"`
	IP                 string                 `json:"ip,omitempty"`
	IDFA               string                 `json:"idfa,omitempty"`
	ADID               string                 `json:"adid,omitempty"`
}

const (
	// event name
	reportIncident = "Report Incident"
	clearIncident  = "Clear Incident"
	appStart       = "App Start"
	latencyReport  = "Latency Report"
)

func sendEvent(eventType, userID, deviceID string, eventProp map[string]interface{}) error {
	env := AssignString(os.Getenv("DeployEnv"), "testing")
	apiKey := GetConfig().AnalyticsCfg.APIKey
	ingestURL := GetConfig().AnalyticsCfg.IngestionURL
	if apiKey == "" || ingestURL == "" {
		return fmt.Errorf("no api key set up for analytics config")
	}

	headers := map[string][]string{
		"Content-Type": {"application/json"},
		"Accept":       {"*/*"},
	}
	epoch := time.Now().UnixNano()
	eventID := int(epoch)

	eventProp["deployEnv"] = env
	event := AmpEvent{
		UserID:          userID,
		DeviceID:        deviceID,
		EventType:       eventType,
		InsertID:        deviceID + strconv.Itoa(eventID),
		SessionID:       -1,
		EpochTime:       epoch,
		EventProperties: eventProp,
		Platform:        "k8s",
	}
	payload := ampPayload{
		APIKey: apiKey,
		Events: []AmpEvent{event},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, ingestURL, bytes.NewBuffer(data))
	req.Header = headers

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp != nil {
		defer resp.Body.Close()
	}

	log.Print("amp analytics status code ", resp.StatusCode)
	if resp.StatusCode > 300 {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		log.Println(buf.String())
		return fmt.Errorf("analytics endpoint returns failure status code %d", resp.StatusCode)
	}
	return nil

}

// AnalyticsReportIncident reports the beginning of an incident
func AnalyticsReportIncident(deviceID, alias, message, description string) {
	go sendEvent(reportIncident, deviceID, deviceID, map[string]interface{}{
		"cluster":     deviceID,
		"alias":       alias,
		"message":     message,
		"description": description,
		"reportedBy":  "pulsar monitor",
		"timestamp":   time.Now(),
	})
}

// AnalyticsClearIncident reports the end of an incident
func AnalyticsClearIncident(deviceID string, durationSeconds int) {
	go sendEvent(clearIncident, deviceID, deviceID, map[string]interface{}{
		"cluster":    deviceID,
		"reportedBy": "pulsar monitor",
		"timestamp":  time.Now(),
	})
}

// AnalyticsAppStart reports a monitor starts
func AnalyticsAppStart(deviceID string) {
	go sendEvent(appStart, deviceID, deviceID, map[string]interface{}{
		"cluster":   deviceID,
		"name":      "pulsar monitor",
		"timestamp": time.Now(),
	})
}

// AnalyticsLatencyReport reports a monitor starts
func AnalyticsLatencyReport(deviceID, name, errorMessage string, latency int, inOrderDelivery, withinLatencyBudget bool) {
	go sendEvent(latencyReport, deviceID, deviceID, map[string]interface{}{
		"cluster":             deviceID,
		"catetory":            "pulsar pub sub latency",
		"name":                name,
		"latencyMs":           latency,
		"timestamp":           time.Now(),
		"inOrderDelivery":     inOrderDelivery,
		"withinLatencyBudget": withinLatencyBudget,
		"error":               errorMessage,
	})
}
