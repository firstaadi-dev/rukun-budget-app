-- Pihak lunas tetap terlihat sampai keluarga menyembunyikannya sendiri.
ALTER TABLE parties ADD COLUMN hidden BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TRIGGER parties_revision AFTER INSERT OR UPDATE OR DELETE ON parties
    FOR EACH ROW EXECUTE FUNCTION bump_family_ledger();
