# station-xiaozhi

Голосовой ассистент [XiaoZhi](https://xiaozhi.me) для Яндекс Станции с root-доступом: «Яндекс» — ассистент, «Алиса» — как обычно.

**Пользователям** нужен готовый релиз из `dist/` и его [README](package/README.md): там установка, привязка, роли и решение проблем.

## Устройство

- `cmd/station-xiaozhi` — программа для станции (Linux ARMv7, Go + cgo: Opus, miniaudio, споттер «Яндекс» из прошивки).
- `internal/voice` — клиент:
  - `station*.go` — состояние Алисы и обработанный звук микрофона из maind (127.0.0.1:9875, :9850), распознавание «Яндекс», приоритет Алисы;
  - `xiaozhi.go` — OTA и WebSocket-протокол XiaoZhi (Opus 16 кГц моно 60 мс вверх, 24 кГц вниз), MCP-инструмент `self.end_conversation`, тишина;
  - `audio.go` — воспроизведение, АРУ микрофона, гудки, голосовые подсказки (`cues/`);
  - `transcript.go` — фразы завершения разговора.
- `package/` — то, что попадает в релиз: установщики для компьютера, `station/` (запуск, автозапуск, установка на станции), пакет yam, роли.
- `tools/build-arm.sh` — сборка ARM-бинарника (закреплённые Zig и Opus), `tools/release.py` — сборка релиза.

На станции: программа и привязка в `/data/station-xiaozhi` (`xiaozhi.json` — MAC и Client-Id, привязанные на xiaozhi.me), автозапуск `/etc/init.d/S99station-xiaozhi`, журнал `client.log`. Файл `/data/station-xiaozhi/debug` включает дамп событий и сырого звука микрофона в `/tmp/station-xiaozhi`.

## Сборка

Нужны Go 1.27.3+, Python 3, make, pkg-config, curl, tar, `adb`. Путь к репозиторию — без пробелов (ограничение Opus/libtool).

```sh
cp config.example.mk config.mk   # указать STATION_IP
make setup                       # Go-модули, Zig, ARM Opus
make release                     # dist/station-xiaozhi-<версия>/, .zip, .yap, SHA256SUMS
make install                     # релиз + установка на STATION_IP
make status | logs | bind | stop | start | uninstall
make check                       # формат, синтаксис скриптов, тесты (нужны Opus и cc на хосте)
```

Тесты можно собрать под ARM и запустить на самой станции: `go test -c` с теми же переменными, что в `tools/build-arm.sh`.

Версия — константа `Version` в `internal/voice/paths.go`.

## Происхождение и лицензия

Основано на [yandex-station-chatgpt-voice](https://github.com/hu553in/yandex-station-chatgpt-voice) (hu553in). У исходного проекта нет лицензии; прежде чем распространять сборки, получите разрешение автора.
