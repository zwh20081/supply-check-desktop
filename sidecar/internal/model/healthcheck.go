package model

// Health-check job/task shapes. The desktop app has no database, so these
// structs exist only to feed the PDF renderer — there is no persistence layer.

// Suite identity.
const (
	HealthCheckProfileVersion = "pricetest-v2"
	HealthCheckChatEndpoint   = "/v1/chat/completions"
)

const (
	HealthCheckJobStatusPending   = "pending"
	HealthCheckJobStatusRunning   = "running"
	HealthCheckJobStatusRetryWait = "retry_wait"
	HealthCheckJobStatusSucceeded = "succeeded"
	HealthCheckJobStatusFailed    = "failed"
	HealthCheckJobStatusCanceled  = "canceled"
)

type HealthCheckJob struct {
	ID                string `json:"id"`
	ProfileVersion    string `json:"profile_version"`
	Endpoint          string `json:"endpoint"`
	RequestedTargets  int    `json:"requested_targets"`
	SkippedTargets    int    `json:"skipped_targets"`
	ChannelID         int    `json:"channel_id"`
	ChannelName       string `json:"channel_name"`
	Model             string `json:"model"`
	Status            string `json:"status"`
	TrustScore        int    `json:"trust_score"`
	Verdict           string `json:"verdict"`
	UpstreamCost      int    `json:"upstream_cost"`
	TotalTasks        int    `json:"total_tasks"`
	CompletedTasks    int    `json:"completed_tasks"`
	FailedTasks       int    `json:"failed_tasks"`
	EstimatedRequests int    `json:"estimated_requests"`
	EstimatedCost     int    `json:"estimated_cost"`
	LastError         string `json:"last_error,omitempty"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
	FinishedAt        int64  `json:"finished_at,omitempty"`
}

type HealthCheckTask struct {
	ID                int    `json:"id"`
	JobID             string `json:"job_id"`
	ChannelID         int    `json:"channel_id"`
	ChannelName       string `json:"channel_name"`
	Model             string `json:"model"`
	Status            string `json:"status"`
	ProbeRunID        int    `json:"probe_run_id,omitempty"`
	TrustScore        int    `json:"trust_score"`
	Verdict           string `json:"verdict"`
	UpstreamCost      int    `json:"upstream_cost"`
	CurrentProbe      string `json:"current_probe,omitempty"`
	CompletedRequests int    `json:"completed_requests"`
	PromptTokens      int    `json:"prompt_tokens"`
	CompletionTokens  int    `json:"completion_tokens"`
	TotalTokens       int    `json:"total_tokens"`
	LastError         string `json:"last_error,omitempty"`
	ErrorClass        string `json:"error_class,omitempty"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
	FinishedAt        int64  `json:"finished_at,omitempty"`
}
