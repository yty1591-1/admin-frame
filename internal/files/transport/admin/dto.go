package admin

// PresignUploadRequest 预签名上传请求。
type PresignUploadRequest struct {
	Category    string `json:"category"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
}

// UploadResponse 服务端上传响应。
type UploadResponse struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
	ETag        string `json:"etag,omitempty"`
}
