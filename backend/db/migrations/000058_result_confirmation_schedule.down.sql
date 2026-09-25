-- Preserve all historical confirmations when reverting the source vocabulary.
UPDATE result_confirmation SET source='auto' WHERE source='scheduled';
ALTER TABLE result_confirmation DROP CONSTRAINT result_confirmation_source_check;
ALTER TABLE result_confirmation ADD CONSTRAINT result_confirmation_source_check
    CHECK (source IN ('manual','auto'));
DROP FUNCTION scheduled_confirmation_classes();
DROP TABLE result_confirmation_schedule;
