package scan

// Report is the result of scanning a skill archive or content for security issues.
type Report struct {
	Status   string    `json:"status"`   // clean, info, warn, critical
	Findings []Finding `json:"findings"`
	Summary  Summary   `json:"summary"`
}

// Finding describes a single matched security rule.
type Finding struct {
	Rule       string `json:"rule"`
	Severity   string `json:"severity"`   // critical, high, medium, info
	Confidence string `json:"confidence"` // high, medium, low
	File       string `json:"file"`
	Line       int    `json:"line"`
	Match      string `json:"match"`
	Message    string `json:"message"`
}

// Summary aggregates finding counts by severity.
type Summary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Info     int `json:"info"`
}
