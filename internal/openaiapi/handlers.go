package openaiapi

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xinzo/higgsfield-proxy/internal/account"
	"github.com/xinzo/higgsfield-proxy/internal/config"
	"github.com/xinzo/higgsfield-proxy/internal/higgs"
	"github.com/xinzo/higgsfield-proxy/internal/media"
)

type Handler struct {
	Cfg  *config.Config
	Pool *account.Pool
	CLI  *higgs.CLI

	jobMu sync.Mutex
	jobs  map[string]*localJob
}

type localJob struct {
	ID        string
	AccountID string
	Model     string
	Status    string
	URL       string
	Err       string
	Created   time.Time
}

func NewHandler(cfg *config.Config, pool *account.Pool, cli *higgs.CLI) *Handler {
	return &Handler{
		Cfg:  cfg,
		Pool: pool,
		CLI:  cli,
		jobs: map[string]*localJob{},
	}
}

func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) writeErr(w http.ResponseWriter, code int, msg, typ, errCode string) {
	h.writeJSON(w, code, ErrorBody{Error: ErrorDetail{Message: msg, Type: typ, Code: errCode}})
}

func (h *Handler) Models(w http.ResponseWriter, r *http.Request) {
	accs, _ := h.Pool.List()
	var models []Model
	seen := map[string]bool{}
	for _, a := range accs {
		if a.Disabled {
			continue
		}
		// refresh token before listing live models
		cp := *a
		if changed, err := h.CLI.EnsureAuth(&cp); err == nil && changed {
			h.Pool.UpdateTokens(cp.ID, cp.AccessToken, cp.RefreshToken, cp.ExpiresAt, cp.TokenType, cp.Scope)
			a = &cp
		}
		for _, kind := range []string{"image", "video"} {
			list, err := h.CLI.ListModels(a, kind)
			if err != nil {
				continue
			}
			for _, m := range list {
				if m.JobType == "" || seen[m.JobType] {
					continue
				}
				seen[m.JobType] = true
				models = append(models, Model{ID: m.JobType, Object: "model", OwnedBy: "higgsfield"})
			}
		}
		if len(models) > 0 {
			break
		}
	}
	// Always merge fallback catalog so canvas still sees full list when CLI auth fails.
	for _, m := range FallbackCatalog {
		if m.ID == "" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		models = append(models, m)
	}
	// virtual models (e.g. seedance_2_0_fast => seedance_2_0 + mode=fast)
	for _, vid := range h.Cfg.VirtualModels() {
		if seen[vid] {
			continue
		}
		seen[vid] = true
		models = append(models, Model{ID: vid, Object: "model", OwnedBy: "higgsfield-proxy"})
	}
	// Do not expose alias strings like "soul->xxx" to canvas model pickers.
	h.writeJSON(w, 200, ModelList{Object: "list", Data: models})
}

func (h *Handler) ImagesGenerations(w http.ResponseWriter, r *http.Request) {
	var req ImageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErr(w, 400, "invalid json body", "invalid_request_error", "invalid_json")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" && !hasAnyMedia(req.MediaRefs) {
		h.writeErr(w, 400, "prompt or image reference is required", "invalid_request_error", "missing_prompt")
		return
	}
	if req.N > 1 {
		h.writeErr(w, 400, "only n=1 supported in v1", "invalid_request_error", "n_not_supported")
		return
	}
	spec := h.Cfg.ResolveModelSpec(req.Model)
	if spec.JobType == "" {
		spec.JobType = h.Cfg.DefaultImageModel
	}
	wait := true
	if req.Wait != nil {
		wait = *req.Wait
	}
	params := ApplyExtraParams(ImageParams(req), spec.ExtraParams)
	opts, err := BuildCreateOpts(h.Cfg.DataDir, spec.JobType, params, req.MediaRefs)
	if err != nil {
		h.writeErr(w, 400, err.Error(), "invalid_request_error", "invalid_media")
		return
	}
	h.runGeneration(w, opts, wait, time.Duration(h.Cfg.ImageTimeoutSec)*time.Second, "image")
}

func (h *Handler) VideosGenerations(w http.ResponseWriter, r *http.Request) {
	var req VideoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErr(w, 400, "invalid json body", "invalid_request_error", "invalid_json")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" && !hasAnyMedia(req.MediaRefs) {
		h.writeErr(w, 400, "prompt or media reference is required", "invalid_request_error", "missing_prompt")
		return
	}
	spec := h.Cfg.ResolveModelSpec(req.Model)
	if spec.JobType == "" || req.Model == "" {
		if req.Model == "" {
			spec = h.Cfg.ResolveModelSpec(h.Cfg.DefaultVideoModel)
		}
	}
	if spec.JobType == "" {
		spec.JobType = h.Cfg.DefaultVideoModel
	}
	wait := true
	if req.Wait != nil {
		wait = *req.Wait
	}
	params := ApplyExtraParams(VideoParams(req), spec.ExtraParams)
	opts, err := BuildCreateOpts(h.Cfg.DataDir, spec.JobType, params, req.MediaRefs)
	if err != nil {
		h.writeErr(w, 400, err.Error(), "invalid_request_error", "invalid_media")
		return
	}
	h.runGeneration(w, opts, wait, time.Duration(h.Cfg.VideoTimeoutSec)*time.Second, "video")
}

// Generate is the canvas-friendly unified endpoint: POST /v1/generate
func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	var req GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeErr(w, 400, "invalid json body", "invalid_request_error", "invalid_json")
		return
	}
	if strings.TrimSpace(req.Prompt) == "" && !hasAnyMedia(req.MediaRefs) {
		h.writeErr(w, 400, "prompt or media reference is required", "invalid_request_error", "missing_prompt")
		return
	}
	spec := h.Cfg.ResolveModelSpec(req.Model)
	if spec.JobType == "" {
		if strings.EqualFold(req.Kind, "video") {
			spec = h.Cfg.ResolveModelSpec(h.Cfg.DefaultVideoModel)
		} else {
			spec = h.Cfg.ResolveModelSpec(h.Cfg.DefaultImageModel)
		}
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	if kind == "" {
		kind = guessKind(spec.JobType)
		if strings.Contains(strings.ToLower(req.Model), "seedance") {
			kind = "video"
		}
	}
	wait := true
	if req.Wait != nil {
		wait = *req.Wait
	}
	var params map[string]string
	var timeout time.Duration
	switch kind {
	case "video":
		params = VideoParams(VideoRequest{Model: spec.JobType, Prompt: req.Prompt, Seconds: req.Seconds, Size: req.Size, Mode: req.Mode, MediaRefs: req.MediaRefs})
		timeout = time.Duration(h.Cfg.VideoTimeoutSec) * time.Second
	default:
		params = ImageParams(ImageRequest{Model: spec.JobType, Prompt: req.Prompt, Size: req.Size, Quality: req.Quality, MediaRefs: req.MediaRefs})
		timeout = time.Duration(h.Cfg.ImageTimeoutSec) * time.Second
		kind = "image"
	}
	params = ApplyExtraParams(params, spec.ExtraParams)
	opts, err := BuildCreateOpts(h.Cfg.DataDir, spec.JobType, params, req.MediaRefs)
	if err != nil {
		h.writeErr(w, 400, err.Error(), "invalid_request_error", "invalid_media")
		return
	}
	h.runGeneration(w, opts, wait, timeout, kind)
}

func (h *Handler) prepareAccount(acc *account.Account) {
	if acc == nil {
		return
	}
	changed, err := h.CLI.EnsureAuth(acc)
	if err == nil && changed {
		h.Pool.UpdateTokens(acc.ID, acc.AccessToken, acc.RefreshToken, acc.ExpiresAt, acc.TokenType, acc.Scope)
	}
	h.refreshCredits(acc)
}

// refreshCredits pulls live balance from upstream and persists to account pool.
func (h *Handler) refreshCredits(acc *account.Account) {
	if acc == nil || acc.ID == "" {
		return
	}
	st, err := h.CLI.AccountStatus(acc)
	if err != nil {
		if higgs.IsAuthError(err) {
			if rerr := higgs.RefreshAccount(acc); rerr == nil {
				h.Pool.UpdateTokens(acc.ID, acc.AccessToken, acc.RefreshToken, acc.ExpiresAt, acc.TokenType, acc.Scope)
				st, err = h.CLI.AccountStatus(acc)
			}
		}
		if err != nil || st == nil {
			return
		}
	}
	h.Pool.UpdateCredits(acc.ID, st.Credits, st.Plan, st.Email)
	acc.Credits = st.Credits
	if st.Email != "" {
		acc.Email = st.Email
	}
	if st.Plan != "" {
		acc.Plan = st.Plan
	}
}

func (h *Handler) refreshCreditsByID(accountID string) {
	if accountID == "" {
		return
	}
	acc, err := h.Pool.Get(accountID)
	if err != nil || acc == nil {
		return
	}
	// auth only if needed, then status
	if changed, err := h.CLI.EnsureAuth(acc); err == nil && changed {
		h.Pool.UpdateTokens(acc.ID, acc.AccessToken, acc.RefreshToken, acc.ExpiresAt, acc.TokenType, acc.Scope)
	}
	h.refreshCredits(acc)
}

func (h *Handler) runGeneration(w http.ResponseWriter, opts higgs.CreateOpts, wait bool, timeout time.Duration, kind string) {
	acc, err := h.Pool.Select()
	if err != nil {
		h.writeErr(w, 503, err.Error(), "server_error", "no_available_account")
		return
	}
	defer h.Pool.Release(acc.ID)

	h.prepareAccount(acc)

	jobID, err := h.CLI.CreateOpts(acc, opts)
	if err != nil {
		// persist tokens if Create refreshed them
		h.Pool.UpdateTokens(acc.ID, acc.AccessToken, acc.RefreshToken, acc.ExpiresAt, acc.TokenType, acc.Scope)
		h.handleUpstreamError(acc.ID, err)
		acc2, err2 := h.Pool.Select()
		if err2 != nil {
			h.writeErr(w, 502, err.Error(), "server_error", "upstream_error")
			return
		}
		defer h.Pool.Release(acc2.ID)
		acc = acc2
		h.prepareAccount(acc)
		jobID, err = h.CLI.CreateOpts(acc, opts)
		if err != nil {
			h.Pool.UpdateTokens(acc.ID, acc.AccessToken, acc.RefreshToken, acc.ExpiresAt, acc.TokenType, acc.Scope)
			h.handleUpstreamError(acc.ID, err)
			h.writeErr(w, 502, err.Error(), "server_error", "upstream_error")
			return
		}
	}
	h.Pool.UpdateTokens(acc.ID, acc.AccessToken, acc.RefreshToken, acc.ExpiresAt, acc.TokenType, acc.Scope)
	// create usually reserves/charges credits — refresh ASAP
	h.refreshCredits(acc)

	h.jobMu.Lock()
	h.jobs[jobID] = &localJob{ID: jobID, AccountID: acc.ID, Model: opts.JobType, Status: "queued", Created: time.Now()}
	h.jobMu.Unlock()

	if !wait {
		h.writeJSON(w, 200, JobResponse{
			ID:        jobID,
			Status:    "queued",
			StatusURL: "/v1/jobs/" + jobID,
			Model:     opts.JobType,
		})
		return
	}

	interval := time.Duration(h.Cfg.PollIntervalMS) * time.Millisecond
	jr, err := h.CLI.Wait(acc, jobID, timeout, interval)
	if err != nil {
		h.jobMu.Lock()
		if j := h.jobs[jobID]; j != nil {
			j.Status = "failed"
			j.Err = err.Error()
		}
		h.jobMu.Unlock()
		h.refreshCredits(acc)
		h.writeErr(w, 502, err.Error(), "server_error", "upstream_error")
		return
	}
	url := jr.ResultURL
	h.jobMu.Lock()
	if j := h.jobs[jobID]; j != nil {
		j.Status = jr.Status
		j.URL = url
	}
	h.jobMu.Unlock()
	// final settle balance after completion/failure
	h.refreshCredits(acc)
	if strings.ToLower(jr.Status) != "completed" || url == "" {
		h.writeJSON(w, 200, JobResponse{ID: jobID, Status: jr.Status, StatusURL: "/v1/jobs/" + jobID, Model: opts.JobType})
		return
	}
	if kind == "video" {
		h.writeJSON(w, 200, VideoResponse{
			Created: time.Now().Unix(),
			Data:    []VideoData{{URL: url}},
			ID:      jobID,
			Status:  "completed",
		})
		return
	}
	h.writeJSON(w, 200, ImageResponse{
		Created: time.Now().Unix(),
		Data:    []ImageData{{URL: url}},
		ID:      jobID,
		Status:  "completed",
	})
}

func (h *Handler) JobGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	id = strings.Trim(id, "/")
	if id == "" {
		h.writeErr(w, 400, "job id required", "invalid_request_error", "missing_id")
		return
	}
	h.jobMu.Lock()
	j := h.jobs[id]
	h.jobMu.Unlock()

	if j != nil && j.AccountID != "" {
		if acc, err := h.Pool.Get(j.AccountID); err == nil {
			if jr, err := h.CLI.Get(acc, id); err == nil {
				st := strings.ToLower(jr.Status)
				if st == "completed" || st == "failed" || st == "nsfw" || jr.ResultURL != "" {
					go h.refreshCreditsByID(j.AccountID)
				}
				h.writeJSON(w, 200, JobResponse{
					ID:     jr.ID,
					Status: jr.Status,
					URL:    jr.ResultURL,
					Model:  jr.JobType,
				})
				return
			}
		}
	}
	accs, _ := h.Pool.List()
	for _, a := range accs {
		if a.Disabled {
			continue
		}
		if jr, err := h.CLI.Get(a, id); err == nil {
			h.writeJSON(w, 200, JobResponse{ID: jr.ID, Status: jr.Status, URL: jr.ResultURL, Model: jr.JobType})
			return
		}
	}
	if j != nil {
		h.writeJSON(w, 200, JobResponse{ID: j.ID, Status: j.Status, URL: j.URL, Model: j.Model})
		return
	}
	h.writeErr(w, 404, "job not found", "invalid_request_error", "not_found")
}

// FilesUpload: POST /v1/files  multipart field "file"
func (h *Handler) FilesUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		h.writeErr(w, 400, "invalid multipart form: "+err.Error(), "invalid_request_error", "invalid_multipart")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		h.writeErr(w, 400, "file field required", "invalid_request_error", "missing_file")
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 200<<20))
	if err != nil {
		h.writeErr(w, 400, "read file failed", "invalid_request_error", "read_error")
		return
	}
	path, err := media.SaveUpload(h.Cfg.DataDir, hdr.Filename, raw)
	if err != nil {
		h.writeErr(w, 500, err.Error(), "server_error", "save_failed")
		return
	}
	h.writeJSON(w, 200, FileUploadResponse{
		ID:       filepath.Base(path),
		Filename: hdr.Filename,
		Path:     path,
		Bytes:    len(raw),
	})
}

func (h *Handler) handleUpstreamError(accountID string, err error) {
	msg := err.Error()
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "not_enough_credits") || strings.Contains(low, "insufficient") || strings.Contains(low, "credit"):
		h.Pool.MarkError(accountID, msg, 2*time.Minute, false)
	case strings.Contains(low, "unauthorized") || strings.Contains(low, "401") || strings.Contains(low, "auth"):
		h.Pool.MarkError(accountID, msg, 10*time.Minute, false)
	case strings.Contains(low, "429") || strings.Contains(low, "rate"):
		h.Pool.MarkError(accountID, msg, 1*time.Minute, false)
	default:
		h.Pool.MarkError(accountID, msg, 15*time.Second, false)
	}
}

func guessKind(model string) string {
	m := strings.ToLower(model)
	if strings.Contains(m, "video") || strings.Contains(m, "seedance") ||
		strings.Contains(m, "kling") || strings.Contains(m, "veo") ||
		strings.Contains(m, "wan2") || strings.Contains(m, "hailuo") ||
		strings.Contains(m, "gemini_omni") || strings.Contains(m, "cinematic_studio") {
		return "video"
	}
	return "image"
}
