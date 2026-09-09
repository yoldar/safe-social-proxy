# Safe Social Proxy

Личный self-hosted портал соцсетей без коротких видео. Оригинальные сайты
блокируются на устройствах через DNS, вместо них — `https://social.eazy.kz`:

| Тайл | Как работает | Что заблокировано |
|---|---|---|
| YouTube (`yt.`) | Собственный мини-фронтенд: поиск + просмотр. Метаданные и стримы извлекает **yt-dlp** на сервере, видео проксируется через `/__proxy/h/…` | Shorts, лента рекомендаций (её просто нет) |
| TikTok (`tt.`) | Реверс-прокси www.tiktok.com с перезаписью хостов | `/foryou`, `/following`; `/` → `/explore` |
| Instagram (`ig.`) | Реверс-прокси best-effort (без логина почти всё закрыто самим Instagram) | `/reels`, `/explore`, `/direct` |
| Wikipedia (`wiki.`) | Реверс-прокси — smoke-тест движка | демо: `Special:Random` |

## Почему YouTube — не «прозрачный» прокси

Веб-плеер YouTube требует PoToken — криптографическую аттестацию BotGuard,
привязанную к origin `youtube.com`. Через переписывающий прокси она не
проходит: googlevideo отвечает 403, а SPA-навигация ломается. Поэтому у
YouTube своя минимальная страница поиска/просмотра, а извлечением стримов
занимается community-поддерживаемый yt-dlp (арсенал обходов обновляется
вместе с пакетом).

**Бот-стена на datacenter-IP.** С IP сервера YouTube отвечает "Sign in to
confirm you're not a bot" при извлечении потока (поиск проходит, а разбор
плеера — нет). Обход без аккаунта — sidecar `bgutil-provider`
(`brainicism/bgutil-ytdlp-pot-provider`), который выдаёт PO-токены; yt-dlp
берёт их через плагин (`YTDLP_POT_BASE_URL=http://bgutil-provider:4416`) и
играет web_safari-поток (HLS). Если провайдер перестанет помогать — крайний
вариант `YTDLP_COOKIES` (Netscape cookies.txt из браузера с логином).

## Архитектура

Один Go-бинарник (`cmd/proxy`), роутинг по поддоменам `*.social.eazy.kz`.
Всё сайто-специфичное — в `config.yaml` (upstream'ы, blocked_paths,
host_map, wildcard-CDN суффиксы). Ядро:

- `internal/proxy` — ReverseProxy (перезапись Host/Location/Set-Cookie/тел
  ответов), реестр сайтов, стрим-эндпоинт `/__proxy/h/<host>/…` со строгим
  allowlist суффиксов (защита от open-proxy);
- `internal/rewrite` — распаковка gzip/brotli, host-Replacer, срез CSP/HSTS,
  JSON-фильтры;
- `internal/youtube` — yt-dlp клиент (поиск/резолв, кэш 10–30 мин,
  semaphore на 3 процесса);
- `internal/server` — host-мультиплексор, портал, blocked-страница.

## Локальный запуск

```bash
go run ./cmd/proxy -config config.yaml
# портал:  http://localhost:8080
# youtube: http://yt.localhost:8080   (нужен yt-dlp в PATH)
# tiktok:  http://tt.localhost:8080
# wiki:    http://wiki.localhost:8080
```

`*.localhost` браузеры резолвят в 127.0.0.1 сами. Переопределение окружением:
`PROXY_DOMAIN`, `PROXY_SCHEME`, `PROXY_LISTEN`, `YTDLP_PATH`.

## Деплой (Dokploy, social.eazy.kz)

- Compose-сервис `proxy` собирается из `Dockerfile` (multi-stage,
  alpine + yt-dlp), сеть `dokploy-network`, порт 8080.
- Домены в Dokploy (Let's Encrypt HTTP-01, все → сервис `proxy:8080`):
  `social.eazy.kz`, `yt.`, `tt.`, `ttm.`, `ig.`, `igstatic.`, `wiki.`,
  `wikim.`, `wikimedia.social.eazy.kz`.
- DNS (ps.kz): нужны ОБЕ записи — `*.social.eazy.kz A 62.171.186.187`
  (wildcard на поддомены) **и отдельно** `social.eazy.kz A 62.171.186.187`
  (wildcard не покрывает голый apex, где живёт портал).

Смоук после деплоя: `make smoke DOMAIN=social.eazy.kz SCHEME=https`.

## Блокировка оригиналов на устройствах

NextDNS / Pi-hole / роутер — denylist:
`youtube.com`, `googlevideo.com`, `ytimg.com`, `instagram.com`,
`cdninstagram.com`, `tiktok.com`, `tiktokcdn.com`.
Сервер прокси должен ходить в интернет с обычным DNS (на VPS так и есть).

## Известные ограничения / план Б

- YouTube: качество — лучший **muxed MP4** (обычно 360p); HLS/DASH в MVP не
  подключены. Если yt-dlp на VPS упрётся в бот-стену — варианты: прокинуть
  cookies (`--cookies`), или заменить тайл на self-hosted Invidious за тем же
  URL.
- Instagram без логина почти пуст — это ограничение самой Меты; тайл честно
  помечен.
- TikTok может периодически требовать «подтверждение» на datacenter-IP.
- Сервис открыт всему интернету: при желании добавить basic-auth средствами
  Dokploy/Traefik или IP-allowlist.
