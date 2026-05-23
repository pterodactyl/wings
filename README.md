[![Logo Image](https://cdn.pterodactyl.io/logos/new/pterodactyl_logo.png)](https://pterodactyl.io)

# Pterodactyl Wings — Fork (Auto-start Disabled)

Это форк официального **Pterodactyl Wings** с одним ключевым изменением: **сервера не запускаются автоматически при старте Wings**.

## Зачем этот форк?

В оригинальном Wings при инициализации происходит восстановление состояния серверов: если контейнер был запущен до перезагрузки, Wings пытается стартовать его снова. При большом количестве контейнеров (например, 19 000+) это приводит к **thundering herd** — одновременному запуску всех серверов, что создаёт огромную нагрузку на ноду.

В этом форке:
- ❌ **Убран авто-старт серверов** при старте Wings.
- ✅ **Docker-образы всё ещё пулятся** и контейнеры создаются через `CreateEnvironment()`.
- ✅ **Re-attach к уже запущенным контейнерам** работает (если сервер был запущен вручную и Wings перезапустился).
- ✅ **Сервера стартуют только вручную** через Panel или API.

## Изменения

| Файл | Изменение |
|------|-----------|
| `cmd/root.go` | Убран вызов `HandlePowerAction(PowerActionStart)` из цикла инициализации. Убрано чтение кешированного состояния `states`, которое использовалось только для авто-старта. |
| `.github/workflows/build.yaml` | Добавлен простой workflow для ручной сборки бинарей под `linux/amd64` и `linux/arm64`. |

## Сборка через GitHub Actions

Проект собирается автоматически через GitHub Actions. Доступно два варианта:

### 1. Быстрая сборка бинаря (кастомный workflow)

Перейди в **Actions** → **Build Custom Wings** → **Run workflow**.

После завершения в артефактах будут:
- `wings_linux_amd64`
- `wings_linux_arm64`

### 2. Сборка + Docker образ (оригинальные workflow)

- Пуш в `develop` → собирается и пушится образ в `ghcr.io`.
- Создание тега `v*` → собирается релиз + Docker образ с тегом.

Собрать локально (на Linux):
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -v -trimpath \
  -ldflags="-s -w -X github.com/pterodactyl/wings/system.Version=custom" \
  -o wings \
  github.com/pterodactyl/wings
```

## Установка

Скачай бинарь из артефактов GitHub Actions, замени им текущий `wings` на ноде и перезапусти сервис:

```bash
chmod +x wings
mv wings /usr/local/bin/wings
systemctl restart wings
```

## Оригинальная документация

* [Panel Documentation](https://pterodactyl.io/panel/1.0/getting_started.html)
* [Wings Documentation](https://pterodactyl.io/wings/1.0/installing.html)
* [Community Guides](https://pterodactyl.io/community/about.html)

## Лицензия

Этот форк наследует лицензию оригинального проекта [Pterodactyl/Wings](https://github.com/pterodactyl/wings).
