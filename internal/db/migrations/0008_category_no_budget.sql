-- Kategorien haben kein eigenes Budget mehr: geplant wird nur noch über das
-- Gesamtbudget bzw. die Pockets, Kategorien ordnen Buchungen nur zu.
ALTER TABLE categories
    DROP COLUMN budget_cents;
