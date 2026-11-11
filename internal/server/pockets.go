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

func (s *Server) readPocket(w http.ResponseWriter, r *http.Request) (db.Pocket, bool) {
	var p db.Pocket
	if !decodeJSON(w, r, &p) {
		return p, false
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Color = strings.ToLower(strings.TrimSpace(p.Color))
	if p.Color == "" {
		p.Color = "#76736e"
	}
	if p.DepositDay == 0 {
		p.DepositDay = 1
	}
	if p.BalanceOn != nil && *p.BalanceOn == "" {
		p.BalanceOn = nil
	}
	switch {
	case p.Name == "" || utf8.RuneCountInString(p.Name) > 100:
		writeError(w, http.StatusBadRequest, "Bitte einen Namen mit höchstens 100 Zeichen angeben")
	case !colorRe.MatchString(p.Color):
		writeError(w, http.StatusBadRequest, "Farbe ungültig")
	case p.MonthlyCents < 0 || p.MonthlyCents > maxCents:
		writeError(w, http.StatusBadRequest, "Monatlicher Betrag ungültig")
	case p.DepositDay < 1 || p.DepositDay > 31:
		writeError(w, http.StatusBadRequest, "Tag des Dauerauftrags muss zwischen 1 und 31 liegen")
	case (p.BalanceCents == nil) != (p.BalanceOn == nil):
		writeError(w, http.StatusBadRequest, "Zum Stand gehört ein Datum")
	case p.BalanceCents != nil && (*p.BalanceCents > maxCents || *p.BalanceCents < -maxCents):
		writeError(w, http.StatusBadRequest, "Stand ungültig")
	case p.BalanceOn != nil && (!validDate(*p.BalanceOn) || *p.BalanceOn > s.today().Format(db.DateLayout)):
		writeError(w, http.StatusBadRequest, "Das Datum des Stands darf nicht in der Zukunft liegen")
	default:
		return p, true
	}
	return p, false
}

func (s *Server) handlePocketCreate(w http.ResponseWriter, r *http.Request) {
	p, ok := s.readPocket(w, r)
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
	p, ok := s.readPocket(w, r)
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
