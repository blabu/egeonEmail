package dto

type Message struct {
	From        string   `json:"from"`
	To          []string `json:"to"`
	Copy        []string `json:"copy"`
	ContentType string   `json:"type"`
	Subject     string   `json:"subject"`
	Data        string   `json:"data"`
}
