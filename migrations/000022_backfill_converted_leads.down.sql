BEGIN;

-- Not reversible: which stage each lead held before it was converted was never
-- recorded, so there is nothing to put back. Sending them all to one stage
-- would be a guess dressed as a rollback. The deal link is untouched either
-- way, so no conversion is lost by leaving this in place.
SELECT 1;

COMMIT;
