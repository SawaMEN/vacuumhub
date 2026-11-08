# Развёртывание и обслуживание VacuumHub

## Предпосылки

- Linux VDS/VPS с Docker Engine и Compose v2; рекомендуется 2 vCPU, 2 ГБ RAM, 20+ ГБ SSD плюс размер архива.
- Домен с A/AAAA на адрес VPS и доступные TCP 80/443 (и UDP 443 для HTTP/3).
- HTTPS-терминация Caddy; не публиковать порт SQLite или внутренний Go API.

## Первый запуск

```bash
git clone https://github.com/SawaMEN/vacuumhub.git
cd vacuumhub
cp .env.example .env
openssl rand -hex 32
# Укажите выведенный секрет в .env как ADMIN_API_TOKEN.
# Замените DOMAIN на свой домен с корректным DNS.
chmod 600 .env
docker compose --env-file .env -f deploy/compose.yaml up -d --build
```

Проверка:

```bash
curl -f https://ВАШ-ДОМЕН/api/v1/health
docker compose --env-file .env -f deploy/compose.yaml ps
docker compose --env-file .env -f deploy/compose.yaml logs --tail=100
```

Ожидаемый ответ health: `{"status":"ok"}`.

Откройте каталог по `https://ВАШ-ДОМЕН/` и админ-панель по `https://ВАШ-ДОМЕН/admin.html`. Административный токен не должен присутствовать в HTML, GitHub или логах. Публичную админку дополнительно ограничьте по VPN/IP.

## Обновление

```bash
git pull --ff-only
docker compose --env-file .env -f deploy/compose.yaml up -d --build
```

Перед изменением миграций базы необходима резервная копия и тест её восстановления. Один и тот же SQLite-файл не должен одновременно использоваться на нескольких серверах по NFS.

## Целостный бэкап

Данные в Docker volume `vacuumhub_data`: база SQLite, WAL и сохранённые файлы. Не копируйте только одиночный `.sqlite` при работающей БД.

Простой последовательный вариант — остановить приложение на время архивации:

```bash
mkdir -p backups
docker compose --env-file .env -f deploy/compose.yaml stop app
CONTAINER_ID=$(docker compose --env-file .env -f deploy/compose.yaml ps -a -q app)
VOLUME=$(docker inspect "$CONTAINER_ID" --format '{{ range .Mounts }}{{ if eq .Destination "/data" }}{{ .Name }}{{ end }}{{ end }}')
test -n "$VOLUME" || { echo 'Не найден том /data'; exit 1; }
docker run --rm -v "$VOLUME:/source:ro" -v "$(pwd)/backups:/backup" alpine:3.21 \
  sh -c 'cd /source && tar czf /backup/vacuumhub-backup.tar.gz .'
docker compose --env-file .env -f deploy/compose.yaml start app
```

Если архивация завершилась с ошибкой, проверьте и вручную запустите `app`. Бэкап храните **вне VPS**, шифруйте и регулярно проверяйте восстановление в отдельном тестовом volume.

## Проверки после развёртывания

- [ ] `/api/v1/health` возвращает HTTP 200.
- [ ] Сайт и админка доступны только по корректному HTTPS.
- [ ] Admin API без ключа отвечает 401.
- [ ] Пакет со статусом pending отсутствует в публичном каталоге.
- [ ] Скачивание pending пакета запрещено.
- [ ] Одобренный пакет скачивается с правильной контрольной суммой.
- [ ] Создание модели и модерация работают через админку.
- [ ] Бэкап и восстановление проверены.
- [ ] Токен и приватные файлы отсутствуют в репозитории и логах.
