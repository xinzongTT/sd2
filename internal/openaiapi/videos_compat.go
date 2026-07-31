package openaiapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xinzo/higgsfield-proxy/internal/higgs"
	"github.com/xinzo/higgsfield-proxy/internal/media"
)

// Videos handles infinite-canvas OpenAI video style:
//
//	POST /v1/videos          create task (multipart FormData or JSON)
//	GET  /v1/videos/{id}     poll task
//
// Also Ark/Seedance rewrite used by infinite-canvas when model contains "seedance":
//
//	POST /v1/contents/generations/tasks
//	GET  /v1/contents/generations/tasks/{id}
func (h *Handler) Videos(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// normalize aliases
	if strings.HasPrefix(path, "/v1/contents/generations/tasks") {
		rest := strings.TrimPrefix(path, "/v1/contents/generations/tasks")
		rest = strings.Trim(rest, "/")
		switch r.Method {
		case http.MethodPost:
			if rest == "" {
				h.videosCreate(w, r)
				return
			}
		case http.MethodGet:
			if rest != "" {
				h.videosGet(w, r, rest)
				return
			}
		}
		h.writeErr(w, 405, "method not allowed", "invalid_request_error", "method_not_allowed")
		return
	}

	path = strings.TrimPrefix(path, "/v1/videos")
	path = strings.Trim(path, "/")

	switch r.Method {
	case http.MethodPost:
		if path == "generations" {
			h.VideosGenerations(w, r)
			return
		}
		if path != "" {
			h.writeErr(w, 405, "method not allowed", "invalid_request_error", "method_not_allowed")
			return
		}
		h.videosCreate(w, r)
	case http.MethodGet:
		if path == "" {
			h.writeErr(w, 400, "video id required", "invalid_request_error", "missing_id")
			return
		}
		h.videosGet(w, r, path)
	default:
		h.writeErr(w, 405, "method not allowed", "invalid_request_error", "method_not_allowed")
	}
}

func (h *Handler) videosCreate(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	var (
		model, prompt, size, mode, resolution string
		seconds                               int
		refs                                  MediaRefs
	)

	if strings.Contains(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(200 << 20); err != nil {
			h.writeErr(w, 400, "invalid multipart: "+err.Error(), "invalid_request_error", "invalid_multipart")
			return
		}
		model = firstForm(r, "model")
		prompt = firstForm(r, "prompt")
		size = firstForm(r, "size")
		mode = firstForm(r, "mode")
		resolution = firstForm(r, "resolution_name", "resolution")
		if v := firstForm(r, "seconds", "duration"); v != "" {
			seconds, _ = strconv.Atoi(v)
		}
		refs.ImageReferences = append(refs.ImageReferences, formValues(r, "input_reference[]", "input_reference")...)
		refs.StartImage = firstForm(r, "first_frame_url", "start_image")
		refs.EndImage = firstForm(r, "last_frame_url", "end_image")
		refs.VideoReferences = append(refs.VideoReferences, formValues(r, "video_reference[]", "video_reference")...)
		refs.AudioReferences = append(refs.AudioReferences, formValues(r, "audio_reference[]", "audio_reference")...)

		if files := h.saveFormFiles(r, "input_reference[]", "input_reference"); len(files) > 0 {
			refs.ImageReferences = append(refs.ImageReferences, files...)
		}
		if files := h.saveFormFiles(r, "video_reference[]", "video_reference"); len(files) > 0 {
			refs.VideoReferences = append(refs.VideoReferences, files...)
		}
		if files := h.saveFormFiles(r, "audio_reference[]", "audio_reference"); len(files) > 0 {
			refs.AudioReferences = append(refs.AudioReferences, files...)
		}
		if files := h.saveFormFiles(r, "first_frame_url", "start_image"); len(files) > 0 && refs.StartImage == "" {
			refs.StartImage = files[0]
		}
		if files := h.saveFormFiles(r, "last_frame_url", "end_image"); len(files) > 0 && refs.EndImage == "" {
			refs.EndImage = files[0]
		}
	} else {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			h.writeErr(w, 400, "invalid json body", "invalid_request_error", "invalid_json")
			return
		}
		model, _ = body["model"].(string)
		prompt, _ = body["prompt"].(string)
		size, _ = body["size"].(string)
		mode, _ = body["mode"].(string)
		if v, ok := body["resolution_name"].(string); ok {
			resolution = v
		}
		if v, ok := body["resolution"].(string); ok && resolution == "" {
			resolution = v
		}
		switch v := body["seconds"].(type) {
		case float64:
			seconds = int(v)
		case string:
			seconds, _ = strconv.Atoi(v)
		}
		if v, ok := body["duration"].(float64); ok && seconds == 0 {
			seconds = int(v)
		}
		if v, ok := body["image"].(string); ok {
			refs.Image = v
		}
		if v, ok := body["start_image"].(string); ok {
			refs.StartImage = v
		}
		if v, ok := body["end_image"].(string); ok {
			refs.EndImage = v
		}
		if v, ok := body["first_frame_url"].(string); ok && refs.StartImage == "" {
			refs.StartImage = v
		}
		if v, ok := body["last_frame_url"].(string); ok && refs.EndImage == "" {
			refs.EndImage = v
		}
		refs.ImageReferences = stringSlice(body["image_references"])
		refs.VideoReferences = stringSlice(body["video_references"])
		refs.AudioReferences = stringSlice(body["audio_references"])
	}

	if strings.TrimSpace(prompt) == "" && !hasAnyMedia(refs) {
		h.writeErr(w, 400, "prompt or media reference is required", "invalid_request_error", "missing_prompt")
		return
	}
	if model == "" {
		model = h.Cfg.DefaultVideoModel
	}

	req := VideoRequest{
		Model:     model,
		Prompt:    prompt,
		Seconds:   seconds,
		Size:      normalizeCanvasSize(size, resolution),
		Mode:      mode,
		MediaRefs: refs,
	}
	params := VideoParams(req)
	if isRatio(size) {
		params["aspect_ratio"] = size
		if resolution != "" {
			params["resolution"] = normalizeRes(resolution)
		}
	}
	spec := h.Cfg.ResolveModelSpec(model)
	if spec.JobType == "" {
		spec.JobType = model
	}
	params = ApplyExtraParams(params, spec.ExtraParams)
	opts, err := BuildCreateOpts(h.Cfg.DataDir, spec.JobType, params, refs)
	if err != nil {
		h.writeErr(w, 400, err.Error(), "invalid_request_error", "invalid_media")
		return
	}

	acc, err := h.Pool.Select()
	if err != nil {
		h.writeErr(w, 503, err.Error(), "server_error", "no_available_account")
		return
	}
	defer h.Pool.Release(acc.ID)
	h.prepareAccount(acc)

	jobID, err := h.CLI.CreateOpts(acc, opts)
	if err != nil {
		h.Pool.UpdateTokens(acc.ID, acc.AccessToken, acc.RefreshToken, acc.ExpiresAt, acc.TokenType, acc.Scope)
		h.handleUpstreamError(acc.ID, err)
		h.writeErr(w, 502, err.Error(), "server_error", "upstream_error")
		return
	}
	h.Pool.UpdateTokens(acc.ID, acc.AccessToken, acc.RefreshToken, acc.ExpiresAt, acc.TokenType, acc.Scope)
	// charge usually happens at create — refresh credits for admin UI
	h.refreshCredits(acc)
	h.jobMu.Lock()
	h.jobs[jobID] = &localJob{ID: jobID, AccountID: acc.ID, Model: spec.JobType, Status: "queued", Created: time.Now()}
	h.jobMu.Unlock()

	h.writeJSON(w, 200, map[string]any{
		"id":       jobID,
		"task_id":  jobID,
		"status":   "queued",
		"progress": 0,
		"model":    model,
		"created":  time.Now().Unix(),
	})
}

func (h *Handler) videosGet(w http.ResponseWriter, r *http.Request, id string) {
	h.jobMu.Lock()
	j := h.jobs[id]
	h.jobMu.Unlock()

	var jr *higgs.JobResult
	if j != nil && j.AccountID != "" {
		if acc, e := h.Pool.Get(j.AccountID); e == nil {
			h.prepareAccount(acc)
			jr, _ = h.CLI.Get(acc, id)
		}
	}
	if jr == nil {
		accs, _ := h.Pool.List()
		for _, a := range accs {
			if a.Disabled {
				continue
			}
			cp := *a
			h.prepareAccount(&cp)
			if got, err := h.CLI.Get(&cp, id); err == nil && got != nil {
				jr = got
				break
			}
		}
	}
	if jr == nil {
		if j != nil {
			status := j.Status
			if status == "" {
				status = "queued"
			}
			out := map[string]any{
				"id":       id,
				"status":   status,
				"progress": progressOf(status),
				"model":    j.Model,
			}
			if j.URL != "" {
				out["url"] = j.URL
				out["video_url"] = j.URL
				out["status"] = "completed"
				out["progress"] = 100
			}
			if j.Err != "" {
				out["status"] = "failed"
				out["error"] = map[string]string{"message": j.Err}
			}
			h.writeJSON(w, 200, out)
			return
		}
		h.writeErr(w, 404, "job not found", "invalid_request_error", "not_found")
		return
	}

	status := strings.ToLower(jr.Status)
	out := map[string]any{
		"id":       jr.ID,
		"status":   mapCanvasStatus(status),
		"progress": progressOf(status),
		"model":    jr.JobType,
	}
	if jr.ResultURL != "" {
		out["url"] = jr.ResultURL
		out["video_url"] = jr.ResultURL
	}
	if status == "failed" || status == "error" || status == "nsfw" || status == "cancelled" {
		out["status"] = "failed"
		out["error"] = map[string]string{"message": "generation " + status}
	}
	h.jobMu.Lock()
	prevStatus := ""
	if j == nil {
		j = &localJob{ID: id, Created: time.Now()}
		h.jobs[id] = j
	} else {
		prevStatus = j.Status
	}
	j.Status = mapCanvasStatus(status)
	j.URL = jr.ResultURL
	j.Model = jr.JobType
	accID := j.AccountID
	h.jobMu.Unlock()

	// refresh credits once when job reaches terminal state
	final := mapCanvasStatus(status)
	if accID != "" && final != prevStatus && (final == "completed" || final == "failed") {
		go h.refreshCreditsByID(accID)
	}

	h.writeJSON(w, 200, out)
}

func (h *Handler) saveFormFiles(r *http.Request, names ...string) []string {
	if r.MultipartForm == nil {
		return nil
	}
	out := []string{}
	for _, name := range names {
		for _, fh := range r.MultipartForm.File[name] {
			f, err := fh.Open()
			if err != nil {
				continue
			}
			raw, err := io.ReadAll(io.LimitReader(f, 200<<20))
			_ = f.Close()
			if err != nil {
				continue
			}
			path, err := media.SaveUpload(h.Cfg.DataDir, fh.Filename, raw)
			if err != nil {
				continue
			}
			out = append(out, path)
		}
	}
	return out
}

func firstForm(r *http.Request, names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(r.FormValue(n)); v != "" {
			return v
		}
	}
	return ""
}

func formValues(r *http.Request, names ...string) []string {
	out := []string{}
	for _, n := range names {
		for _, v := range r.Form[n] {
			v = strings.TrimSpace(v)
			if v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func stringSlice(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	case string:
		if strings.TrimSpace(t) != "" {
			return []string{t}
		}
	}
	return nil
}

func isRatio(s string) bool {
	s = strings.TrimSpace(s)
	return s == "16:9" || s == "9:16" || s == "1:1" || s == "4:3" || s == "3:4" || s == "21:9" || s == "adaptive" || s == "auto"
}

func normalizeRes(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "low" {
		return "480p"
	}
	if v == "high" || v == "medium" || v == "auto" {
		return "720p"
	}
	if v != "" && !strings.HasSuffix(v, "p") {
		return v + "p"
	}
	return v
}

func normalizeCanvasSize(size, resolution string) string {
	size = strings.TrimSpace(size)
	if size == "" {
		switch normalizeRes(resolution) {
		case "480p":
			return "854x480"
		case "1080p":
			return "1920x1080"
		default:
			return "1280x720"
		}
	}
	if isRatio(size) {
		res := normalizeRes(resolution)
		if res == "" {
			res = "720p"
		}
		switch size {
		case "9:16":
			if res == "1080p" {
				return "1080x1920"
			}
			if res == "480p" {
				return "480x854"
			}
			return "720x1280"
		case "1:1":
			if res == "1080p" {
				return "1080x1080"
			}
			if res == "480p" {
				return "640x640"
			}
			return "720x720"
		default:
			if res == "1080p" {
				return "1920x1080"
			}
			if res == "480p" {
				return "854x480"
			}
			return "1280x720"
		}
	}
	return size
}

func mapCanvasStatus(status string) string {
	switch strings.ToLower(status) {
	case "completed", "complete", "done", "success", "succeeded":
		return "completed"
	case "failed", "fail", "error", "nsfw", "cancelled", "canceled":
		return "failed"
	case "in_progress", "running", "processing":
		return "in_progress"
	default:
		return "queued"
	}
}

func progressOf(status string) int {
	switch mapCanvasStatus(status) {
	case "completed":
		return 100
	case "failed":
		return 0
	case "in_progress":
		return 50
	default:
		return 10
	}
}
