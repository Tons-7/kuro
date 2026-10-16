-- +goose Up

-- Raw scores above 1 read as hand assignments and froze guesses; exactly 1 is the hand's, so rescale.
UPDATE local_file SET confidence = min(confidence / 40.0, 0.99) WHERE confidence > 1;

-- +goose Down
SELECT 1;
