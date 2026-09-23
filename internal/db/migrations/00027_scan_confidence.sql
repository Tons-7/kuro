-- +goose Up

-- The scanner stored raw match scores (6 and up) as confidence, which reads as
-- a hand assignment (1) and froze every guess through later rescans. Exactly 1
-- is the hand's; anything above was the scanner's.
UPDATE local_file SET confidence = min(confidence / 40.0, 0.99) WHERE confidence > 1;

-- +goose Down
SELECT 1;
