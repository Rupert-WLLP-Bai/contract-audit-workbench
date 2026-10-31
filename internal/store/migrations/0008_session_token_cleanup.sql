-- Old plaintext sessions are invalid after the digest cutover.
-- Stop old application instances before applying this migration.
DO $$
BEGIN
 ALTER TABLE sessions NO FORCE ROW LEVEL SECURITY;
 DELETE FROM sessions WHERE token COLLATE "C" ~ '^s[0-9a-f]{16}$';
 ALTER TABLE sessions FORCE ROW LEVEL SECURITY;
END $$;
