-- +goose Up

-- 10 GiB, about seven episodes at 1080p WEB.
UPDATE setting SET value = '10737418240' WHERE key = 'cache.budget_bytes';

-- +goose Down
UPDATE setting SET value = '42949672960' WHERE key = 'cache.budget_bytes';
