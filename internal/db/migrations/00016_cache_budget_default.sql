-- +goose Up

-- The default cache budget drops to 5 GiB; only rows still on the old default move, not anyone's choice.
UPDATE setting SET value = '5368709120'
WHERE key = 'cache.budget_bytes' AND value = '10737418240';

-- +goose Down
UPDATE setting SET value = '10737418240'
WHERE key = 'cache.budget_bytes' AND value = '5368709120';
