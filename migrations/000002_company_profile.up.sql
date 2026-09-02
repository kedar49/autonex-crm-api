-- Migration: Add company_profiles extension table for rich VIGIL company profile data

CREATE TABLE IF NOT EXISTS company_profiles (
    company_id         UUID PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
    tagline            TEXT,
    description        TEXT,
    primary_color      VARCHAR(9) DEFAULT '#6366f1',
    banner_url         TEXT,
    plant_locations    JSONB NOT NULL DEFAULT '[]'::jsonb,
    ai_detections      TEXT[] NOT NULL DEFAULT '{}',
    hardware_specs     JSONB NOT NULL DEFAULT '{}'::jsonb,
    amc_status         TEXT NOT NULL DEFAULT 'none' CHECK (amc_status IN ('active', 'pending_renewal', 'expired', 'none')),
    amc_start_date     DATE,
    amc_end_date       DATE,
    amc_value          NUMERIC(15,2) DEFAULT 0,
    custom_sections    JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_company_profiles_amc_status ON company_profiles(amc_status);

DROP TRIGGER IF EXISTS set_company_profiles_updated_at ON company_profiles;
CREATE TRIGGER set_company_profiles_updated_at
BEFORE UPDATE ON company_profiles
FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();
