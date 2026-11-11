package server

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"nexoraplan/internal/db"
)

// Pockets: Unterkonten wie die „Pockets“ der C24 Bank. Auf jedes geht monatlich
// ein fester Betrag; ihre Summe ist das Gesamtbudget.

func (s *Server) handlePockets(w http.ResponseWriter, r *http.Request) {
	list, err := s.repo.Pockets(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func readPocket(w http.ResponseWriter, r *http.Request) (db.Pocket, bool) {
	var p db.Pocket
	if !decodeJSON(w, r, &p) {
		return p, false
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Color = strings.ToLower(strings.TrimSpace(p.Color))
	if p.Color == "" {
		p.Color = "#76736e"
	}
	switch {
	case p.Name == "" || utf8.RuneCountInString(p.Name) > 100:
		writeError(w, http.StatusBadRequest, "Bitte einen Namen mit höchstens 100 Zeichen angeben")
	case !colorRe.MatchString(p.Color):
		writeError(w, http.StatusBadRequest, "Farbe ungültig")
	case p.MonthlyCents < 0 || p.MonthlyCents > maxCents:
		writeError(w, http.StatusBadRequest, "Monatlicher Betrag ungültig")
	default:
		return p, true
	}
	return p, false
}

func (s *Server) handlePocketCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := readPocket(w, r)
	if !ok {
		return
	}
	p, err := s.repo.CreatePocket(r.Context(), p)
	if errors.Is(err, db.ErrExists) {
		writeError(w, http.StatusConflict, "Dieses Pocket gibt es schon")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handlePocketUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, ok := readPocket(w, r)
	if !ok {
		return
	}
	p.ID = id
	err := s.repo.UpdatePocket(r.Context(), p)
	if errors.Is(err, db.ErrExists) {
		writeError(w, http.StatusConflict, "Dieses Pocket gibt es schon")
		return
	}
	if err != nil {
		dbError(w, r, err, "Pocket")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handlePocketDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.repo.DeletePocket(r.Context(), id); err != nil {
		dbError(w, r, err, "Pocket")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// checkPocket: nil (ohne Pocket) oder ein vorhandenes Pocket.
func (s *Server) checkPocket(w http.ResponseWriter, r *http.Request, id *uint64) bool {
	if id == nil {
		return true
	}
	exists, err := s.repo.PocketExists(r.Context(), *id)
	if err != nil {
		internalError(w, r, err)
		return false
	}
	if !exists {
		writeError(w, http.StatusBadRequest, "Pocket nicht gefunden")
		return false
	}
	return true
}
