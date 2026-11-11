package server

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	_ "image/gif" // Formate fürs Hochladen
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"nexoraplan/internal/db"
)

const (
	logoSize      = 128     // Kantenlänge des gespeicherten Logos
	maxLogoUpload = 5 << 20 // 5 MB
	maxLogoPixels = 40e6    // gegen Bilder, die beim Entpacken riesig werden
)

var errLogoFormat = errors.New("Bitte ein Bild als PNG, JPG, GIF oder WebP hochladen")

// normalizeLogo liest ein Bild und rechnet es neu: höchstens 128×128, mittig
// auf transparentem Grund, als PNG. Gespeichert wird nie die hochgeladene
// Datei selbst – so kommt nichts mit, was im Bild versteckt war.
func normalizeLogo(r io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxLogoUpload+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxLogoUpload {
		return nil, errors.New("Das Bild ist größer als 5 MB")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil, errLogoFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || float64(cfg.Width)*float64(cfg.Height) > maxLogoPixels {
		return nil, errors.New("Das Bild hat zu viele Pixel")
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, errLogoFormat
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := min(float64(logoSize)/float64(w), float64(logoSize)/float64(h), 1)
	tw, th := max(int(float64(w)*scale+0.5), 1), max(int(float64(h)*scale+0.5), 1)
	dst := image.NewNRGBA(image.Rect(0, 0, logoSize, logoSize))
	draw.Draw(dst, dst.Bounds(), image.Transparent, image.Point{}, draw.Src)
	off := image.Pt((logoSize-tw)/2, (logoSize-th)/2)
	xdraw.CatmullRom.Scale(dst, image.Rectangle{Min: off, Max: off.Add(image.Pt(tw, th))}, src, b, xdraw.Over, nil)
	var out bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, dst); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// PUT /api/recurring/{id}/logo – Body ist das Bild selbst (Content-Type image/…).
// Fremde Seiten können so keinen Upload auslösen: ein Formular sendet nie image/…,
// fetch von fremden Seiten bräuchte einen Preflight, den der Server nicht erlaubt.
func (s *Server) handleRecurringLogoPut(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	switch r.Header.Get("Content-Type") {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		writeError(w, http.StatusUnsupportedMediaType, errLogoFormat.Error())
		return
	}
	logo, err := normalizeLogo(http.MaxBytesReader(w, r.Body, maxLogoUpload+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.repo.SetRecurringLogo(r.Context(), id, logo); err != nil {
		dbError(w, r, err, "Abo / Vertrag")
		return
	}
	x, err := s.repo.RecurringByID(r.Context(), id)
	if err != nil {
		dbError(w, r, err, "Abo / Vertrag")
		return
	}
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) handleRecurringLogoDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.repo.SetRecurringLogo(r.Context(), id, nil); err != nil {
		dbError(w, r, err, "Abo / Vertrag")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/recurring/{id}/logo?v=<logo_version> – die Adresse ändert sich mit
// jedem neuen Logo, darum darf der Browser es dauerhaft zwischenspeichern.
func (s *Server) handleRecurringLogoGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	logo, err := s.repo.RecurringLogo(r.Context(), id)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Write(logo)
}
