package models

type LogEntry struct {
	Time       int64             `json:"time"`
	Host       string            `json:"host"`
	Source     string            `json:"source"`
	SourceType string            `json:"sourcetype"`
	Event      string            `json:"event"`
	Fields     map[string]string `json:"fields,omitempty"`
}
