-- The original immutable-scheme trigger returned NEW for DELETE. PostgreSQL
-- defines NEW as NULL in a DELETE trigger, which silently cancelled deletion
-- of drafts while still returning a successful command. Published rows remain
-- protected; drafts now return OLD and are actually removed.
CREATE OR REPLACE FUNCTION reject_published_scheme_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'published' THEN
        RAISE EXCEPTION 'published schemes are immutable' USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END
$$;
