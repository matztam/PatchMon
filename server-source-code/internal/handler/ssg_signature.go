package handler

// fork: signed SCAP content. Agents only install a datastream whose minisign
// signature (filename + ".minisig", next to it in the content directory)
// verifies against the key built into them.

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"github.com/PatchMon/PatchMon/server-source-code/internal/ssgcontent"
)

// SSGSignature handles GET /compliance/ssg-signature/{filename} (agent auth).
func (h *ComplianceHandler) SSGSignature(w http.ResponseWriter, r *http.Request) {
	if !h.authenticateAgent(w, r) {
		return
	}
	filename := chi.URLParam(r, "filename")
	if !ssgcontent.FilenameRe.MatchString(filename) {
		Error(w, http.StatusBadRequest, "Invalid filename")
		return
	}
	sigPath := filepath.Join(h.ssgContentDir, filename+".minisig")
	data, err := os.ReadFile(sigPath)
	if err != nil {
		Error(w, http.StatusNotFound, "Signature not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}
