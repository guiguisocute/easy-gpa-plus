-- 归组是一次性调用，逐张识别的进度条早就满了，界面上几分钟一动不动。这里存一份
-- 归组过程中已经成型的候选标题，供前端在等待期间回显。
ALTER TABLE ai_batch ADD COLUMN compose_preview TEXT NOT NULL DEFAULT '';
