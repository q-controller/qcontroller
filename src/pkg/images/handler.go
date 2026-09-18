package images

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	imageservice "github.com/q-controller/qcontroller/src/generated/oapi"
)

// maxImageIDLen bounds the "id" form field; anything longer is not an image
// name, and the part is read into memory.
const maxImageIDLen = 256

type Handler struct {
	imageCli ImageClient
}

func (h *Handler) PostV1Images(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Walk the multipart body part by part and stream the file straight to
	// the registry.
	mr, mrErr := r.MultipartReader()
	if mrErr != nil {
		http.Error(w, "failed to parse form: "+mrErr.Error(), http.StatusBadRequest)
		return
	}

	var id string
	for {
		part, partErr := mr.NextPart()
		if errors.Is(partErr, io.EOF) {
			if id == "" {
				http.Error(w, "Missing id parameter", http.StatusBadRequest)
			} else {
				http.Error(w, "Missing file parameter", http.StatusBadRequest)
			}
			return
		}
		if partErr != nil {
			http.Error(w, "failed to parse form: "+partErr.Error(), http.StatusBadRequest)
			return
		}

		switch part.FormName() {
		case "id":
			raw, readErr := io.ReadAll(io.LimitReader(part, maxImageIDLen+1))
			if readErr != nil {
				http.Error(w, "failed to read id: "+readErr.Error(), http.StatusBadRequest)
				return
			}
			if len(raw) == 0 || len(raw) > maxImageIDLen {
				http.Error(w, "Missing id parameter", http.StatusBadRequest)
				return
			}
			id = string(raw)
		case "file":
			// The client sends id before file (schema order); anything else
			// would force buffering the file, so refuse it instead.
			if id == "" {
				http.Error(w, "Missing id parameter (must precede file)", http.StatusBadRequest)
				return
			}
			if uploadErr := h.imageCli.Upload(r.Context(), id, part); uploadErr != nil {
				http.Error(w, "Failed to upload file: "+uploadErr.Error(), http.StatusInternalServerError)
				return
			}
			return
		}
		// Unknown parts are skipped; NextPart discards the remainder.
	}
}

//nolint:revive // openapi-generated interface contract
func (h *Handler) DeleteV1ImagesImageId(w http.ResponseWriter, r *http.Request, imageId string) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	respErr := h.imageCli.Remove(r.Context(), imageId)
	if respErr != nil {
		http.Error(w, "Failed to remove image: "+respErr.Error(), http.StatusInternalServerError)
		return
	}
}

func (h *Handler) GetV1Images(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	images, respErr := h.imageCli.List(r.Context())
	if respErr != nil {
		http.Error(w, "Failed to list images: "+respErr.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	resp := map[string]any{
		"images": images,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.WarnContext(r.Context(), "Failed to encode JSON response", "error", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func CreateHandler(cli ImageClient, mux *http.ServeMux) http.Handler {
	return imageservice.HandlerFromMux(&Handler{
		imageCli: cli,
	}, mux)
}
