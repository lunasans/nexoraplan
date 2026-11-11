-- Gebuchte Abos/Verträge ohne Kategorie bekommen die Kategorie ihres Vertrags.
-- Bis hierhin zog eine nachträglich zugeordnete Kategorie nicht mit, die
-- schon gebuchten Buchungen standen weiter unter „Ohne Kategorie“.
UPDATE transactions t
JOIN recurring r ON r.id = t.recurring_id
SET t.category_id = r.category_id
WHERE t.category_id IS NULL AND r.category_id IS NOT NULL;
