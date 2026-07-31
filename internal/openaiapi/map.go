package openaiapi

import (
	"fmt"
	"strings"

	"github.com/xinzo/higgsfield-proxy/internal/higgs"
	"github.com/xinzo/higgsfield-proxy/internal/media"
)

// SizeToAspect maps OpenAI-like sizes to Higgsfield aspect_ratio.
func SizeToAspect(size string) string {
	s := strings.TrimSpace(strings.ToLower(size))
	switch s {
	case "", "1024x1024", "512x512", "1536x1536", "1:1":
		return "1:1"
	case "1792x1024", "1344x768", "1280x720", "16:9":
		return "16:9"
	case "1024x1792", "768x1344", "720x1280", "9:16":
		return "9:16"
	case "1024x768", "4:3":
		return "4:3"
	case "768x1024", "3:4":
		return "3:4"
	case "3:2":
		return "3:2"
	case "2:3":
		return "2:3"
	default:
		parts := strings.Split(s, "x")
		if len(parts) == 2 {
			var w, h int
			_, _ = fmt.Sscanf(parts[0], "%d", &w)
			_, _ = fmt.Sscanf(parts[1], "%d", &h)
			if w > 0 && h > 0 {
				if w == h {
					return "1:1"
				}
				if w > h {
					if float64(w)/float64(h) > 1.5 {
						return "16:9"
					}
					return "4:3"
				}
				if float64(h)/float64(w) > 1.5 {
					return "9:16"
				}
				return "3:4"
			}
		}
		return "1:1"
	}
}

func ImageParams(req ImageRequest) map[string]string {
	params := map[string]string{}
	if strings.TrimSpace(req.Prompt) != "" {
		params["prompt"] = req.Prompt
	}
	if req.Size != "" {
		params["aspect_ratio"] = SizeToAspect(req.Size)
	} else {
		params["aspect_ratio"] = "1:1"
	}
	q := strings.TrimSpace(req.Quality)
	if q == "" {
		q = "1.5k"
	}
	switch strings.ToLower(q) {
	case "standard", "720p", "1.5k":
		params["quality"] = "1.5k"
	case "hd", "2k", "high":
		params["quality"] = "2k"
	case "1k", "basic", "low":
		params["quality"] = "1k"
	case "4k", "ultra":
		params["quality"] = "4k"
	default:
		params["quality"] = q
	}
	return params
}

func VideoParams(req VideoRequest) map[string]string {
	params := map[string]string{}
	if strings.TrimSpace(req.Prompt) != "" {
		params["prompt"] = req.Prompt
	}
	if req.Size != "" {
		params["aspect_ratio"] = SizeToAspect(req.Size)
		low := strings.ToLower(req.Size)
		if strings.Contains(low, "1080") || req.Size == "1920x1080" {
			params["resolution"] = "1080p"
		} else if strings.Contains(low, "720") || req.Size == "1280x720" {
			params["resolution"] = "720p"
		} else if strings.Contains(low, "480") {
			params["resolution"] = "480p"
		} else if strings.Contains(low, "4k") {
			params["resolution"] = "4k"
		}
	}
	sec := req.Seconds
	// Seedance (and most Higgsfield video jobs) reject duration < 4.
	// Canvas often sends seconds=1; clamp to a valid range.
	if sec > 0 && sec < 4 {
		sec = 4
	}
	if sec > 15 {
		sec = 15
	}
	if sec > 0 {
		params["duration"] = fmt.Sprintf("%d", sec)
	}
	if m := strings.TrimSpace(req.Mode); m != "" {
		params["mode"] = strings.ToLower(m)
	}
	return params
}

// ApplyExtraParams merges fixed params from virtual models (e.g. mode=fast).
func ApplyExtraParams(params map[string]string, extra map[string]string) map[string]string {
	if params == nil {
		params = map[string]string{}
	}
	for k, v := range extra {
		if strings.TrimSpace(v) == "" {
			continue
		}
		// explicit request fields already set win over defaults, except mode from virtual model should win if not set
		if _, ok := params[k]; !ok || k == "mode" {
			params[k] = v
		}
	}
	return params
}

// BuildCreateOpts resolves media refs and builds CLI create options.
func BuildCreateOpts(dataDir, jobType string, single map[string]string, refs MediaRefs) (higgs.CreateOpts, error) {
	opts := higgs.CreateOpts{
		JobType:    jobType,
		Params:     single,
		MultiFlags: map[string][]string{},
	}

	// image references
	imgs := append([]string{}, refs.ImageReferences...)
	if refs.Image != "" {
		imgs = append([]string{refs.Image}, imgs...)
	}
	imgs, err := media.ResolveMany(dataDir, imgs)
	if err != nil {
		return opts, err
	}
	if len(imgs) == 1 {
		// single --image is widely accepted
		opts.MultiFlags["image"] = imgs
	} else if len(imgs) > 1 {
		opts.MultiFlags["image-references"] = imgs
	}

	if refs.StartImage != "" {
		v, err := media.Resolve(dataDir, refs.StartImage)
		if err != nil {
			return opts, err
		}
		opts.MultiFlags["start-image"] = []string{v}
	}
	if refs.EndImage != "" {
		v, err := media.Resolve(dataDir, refs.EndImage)
		if err != nil {
			return opts, err
		}
		opts.MultiFlags["end-image"] = []string{v}
	}

	vids := append([]string{}, refs.VideoReferences...)
	if refs.Video != "" {
		vids = append([]string{refs.Video}, vids...)
	}
	vids, err = media.ResolveMany(dataDir, vids)
	if err != nil {
		return opts, err
	}
	if len(vids) == 1 {
		opts.MultiFlags["video"] = vids
	} else if len(vids) > 1 {
		opts.MultiFlags["video-references"] = vids
	}

	auds := append([]string{}, refs.AudioReferences...)
	if refs.Audio != "" {
		auds = append([]string{refs.Audio}, auds...)
	}
	auds, err = media.ResolveMany(dataDir, auds)
	if err != nil {
		return opts, err
	}
	if len(auds) == 1 {
		opts.MultiFlags["audio"] = auds
	} else if len(auds) > 1 {
		opts.MultiFlags["audio-references"] = auds
	}

	return opts, nil
}

func hasAnyMedia(r MediaRefs) bool {
	return r.Image != "" || len(r.ImageReferences) > 0 ||
		r.StartImage != "" || r.EndImage != "" ||
		r.Video != "" || len(r.VideoReferences) > 0 ||
		r.Audio != "" || len(r.AudioReferences) > 0
}
