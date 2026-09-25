CREATE OR REPLACE FUNCTION reject_published_scheme_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'published' THEN
        RAISE EXCEPTION 'published schemes are immutable' USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$$;
