-- Токены сессий больше не храним открытым текстом: в sessions.token лежит
-- hex(SHA-256) от токена из cookie (auth.hashSessionToken). Существующие сессии
-- хэшируем на месте — пользователей не разлогинивает. Токены — base64url ASCII,
-- convert_to(…,'UTF8') даёт те же байты, что Go []byte(token).
UPDATE sessions SET token = encode(sha256(convert_to(token, 'UTF8')), 'hex');
