BEGIN;

ALTER TABLE deals ADD COLUMN IF NOT EXISTS lead_id uuid REFERENCES public.leads(id);

COMMIT;
