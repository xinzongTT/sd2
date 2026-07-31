package openaiapi

type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	OwnedBy string `json:"owned_by"`
}

type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// MediaRefs is shared by image/video requests for canvas/proxy clients.
// Each item may be: local path, upload UUID, http(s) URL, or data URL.
type MediaRefs struct {
	// Image is a single image shorthand (OpenAI-style / first reference).
	Image string `json:"image,omitempty"`
	// ImageReferences multi image refs.
	ImageReferences []string `json:"image_references,omitempty"`
	// StartImage / EndImage for i2v first-last frame.
	StartImage string `json:"start_image,omitempty"`
	EndImage   string `json:"end_image,omitempty"`
	// Video / VideoReferences
	Video           string   `json:"video,omitempty"`
	VideoReferences []string `json:"video_references,omitempty"`
	// Audio / AudioReferences
	Audio           string   `json:"audio,omitempty"`
	AudioReferences []string `json:"audio_references,omitempty"`
}

type ImageRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	N       int    `json:"n"`
	Size    string `json:"size"`
	Quality string `json:"quality"`
	MediaRefs
	// Wait defaults true for images.
	Wait *bool `json:"wait,omitempty"`
}

type ImageData struct {
	URL string `json:"url"`
}

type ImageResponse struct {
	Created int64       `json:"created"`
	Data    []ImageData `json:"data"`
	ID      string      `json:"id,omitempty"`
	Status  string      `json:"status,omitempty"`
}

type VideoRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Seconds int    `json:"seconds"`
	Size    string `json:"size"`
	// Mode e.g. seedance: std|fast
	Mode string `json:"mode,omitempty"`
	MediaRefs
	Wait *bool `json:"wait"`
}

type VideoData struct {
	URL string `json:"url"`
}

type VideoResponse struct {
	Created int64       `json:"created"`
	Data    []VideoData `json:"data"`
	ID      string      `json:"id,omitempty"`
	Status  string      `json:"status,omitempty"`
}

type JobResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	StatusURL string `json:"status_url,omitempty"`
	URL       string `json:"url,omitempty"`
	Model     string `json:"model,omitempty"`
}

// GenerateRequest is the canvas-friendly unified endpoint body.
type GenerateRequest struct {
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
	Kind    string `json:"kind,omitempty"` // image|video|auto
	Size    string `json:"size,omitempty"`
	Quality string `json:"quality,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
	Mode    string `json:"mode,omitempty"`
	MediaRefs
	Wait *bool `json:"wait,omitempty"`
}

type FileUploadResponse struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Bytes    int    `json:"bytes"`
}
