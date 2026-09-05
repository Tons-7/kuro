-- +goose Up

-- 5 GiB. Ten gigabytes of episodes nobody had asked to keep was a lot to take
-- from someone's disk by default, and the cache exists to make a rewatch
-- instant rather than to archive.
--
-- Only rows still holding the previous default move: a budget anyone chose
-- themselves is their decision, and migration 00007 already overwrote one such
-- choice once.
UPDATE setting SET value = '5368709120'
WHERE key = 'cache.budget_bytes' AND value = '10737418240';

-- +goose Down
UPDATE setting SET value = '10737418240'
WHERE key = 'cache.budget_bytes' AND value = '5368709120';
