package webhook

// Payload is the expected body for a shuttle webhook.
// We define our own shape (not raw GitHub Actions event) to decouple from GHA format.
type Payload struct {
	Ref       string   `json:"ref"`
	CommitSHA string   `json:"commit_sha"`
	Repo      string   `json:"repo"`
	Services  []string `json:"services"` // empty = deploy all changed services
}
