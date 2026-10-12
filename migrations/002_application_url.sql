-- Add optional Web entry addresses without rebuilding existing application data.
ALTER TABLE applications ADD COLUMN IF NOT EXISTS url VARCHAR(2048) NOT NULL DEFAULT '';
