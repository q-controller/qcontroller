package images

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
)

type Handler struct {
	imageCli ImageClient
}

func (h *Handler) Post(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parseErr := r.ParseMultipartForm(10 << 20)
	if parseErr != nil {
		http.Error(w, fmt.Sprintf("failed to parse form: %s", parseErr.Error()), http.StatusBadRequest)
		return
	}

	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "Missing id parameter", http.StatusBadRequest)
		return
	}

	file, _, fileErr := r.FormFile("file")
	if fileErr != nil {
		http.Error(w, "Failed to retrieve file: "+fileErr.Error(), http.StatusBadRequest)
		return
	}

	defer func() {
		if err := file.Close(); err != nil {
			slog.Warn("Failed to close file", "error", err)
		}
	}()

	if uploadErr := h.imageCli.Upload(r.Context(), id, file); uploadErr != nil {
		http.Error(w, "Failed to upload file: "+uploadErr.Error(), http.StatusInternalServerError)
		return
	}
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	imageId, ok := pathParams["imageId"]
	if !ok || imageId == "" {
		http.Error(w, "Missing imageId parameter", http.StatusBadRequest)
		return
	}

	respErr := h.imageCli.Remove(r.Context(), imageId)
	if respErr != nil {
		http.Error(w, "Failed to remove image: "+respErr.Error(), http.StatusInternalServerError)
		return
	}
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request, pathParams map[string]string) {
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

	response := map[string][]string{"images": images}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Warn("Failed to encode JSON response", "error", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
	}
}

func CreateHandler(cli ImageClient) (*Handler, error) {
	return &Handler{
		imageCli: cli,
	}, nil
}
