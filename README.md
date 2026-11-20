# Trip Service

Сервис создаёт поездки, возвращает их по ID и завершает, принимает координаты
и отдаёт маршрут. По поездкам, от которых давно не было координат, он сам
просит устройство водителя прислать координату через Push Service. HTTP API задан в
[`contracts/openapi/trip-service.openapi.yaml`](contracts/openapi/trip-service.openapi.yaml),
данные хранятся в PostgreSQL. Текущая архитектура описана в
[`docs/architecture.md`](docs/architecture.md). Материалы курса сохранены в
[`docs/course/homework/`](docs/course/homework/) и
[`docs/course/lectures-and-practice/`](docs/course/lectures-and-practice/).

## Структура репозитория

```text
cmd/                  точка входа сервиса
internal/             реализация сервиса и тесты
migrations/           SQL-миграции goose
api/                  конфигурация генерации HTTP API
contracts/            контракты, используемые сервисом
docs/
  architecture.md     описание текущей архитектуры
  course/
    homework/         задания, требования и справочные материалы курса
    lectures-and-practice/  лекции и практические задания
go.mod, go.sum        модуль Go и зависимости
Makefile              запуск, миграции, генерация и тесты
Dockerfile            сборка контейнера сервиса
environment.toml      состав локального окружения
.env.example          пример конфигурации
README.md             описание и инструкция запуска
```

Все лабораторные последовательно развивают один сервис. Копия контрактов в
`docs/course/homework/contracts/` относится к материалам курса; генерация кода
сервиса использует корневой `contracts/`.

## Архитектура

```text
HTTP-хендлер → usecase → домен
                  │
                  └─ контракт репозитория ← PostgreSQL-репозиторий

worker/positions (сканер → очередь → воркеры) → gateway/push → Push Service
```

У создания, чтения и завершения поездки, сохранения координаты и получения
маршрута свои пакеты на слоях HTTP и usecase.
HTTP разбирает запросы и преобразует ошибки через `http/domainerror`. Usecase
управляет транзакциями и вызывает доменные правила. Домен отвечает за валидацию
поездки и переход статуса. Репозиторий выполняет SQL через пул или транзакцию
из контекста.

Интерфейсы лежат в `contract.go` потребителя. Хендлер описывает нужный ему
usecase, а usecase — нужные ему операции репозитория и менеджера транзакций.
Так каждый пакет зависит только от операций, которые вызывает, и тестируется
с заменой этих зависимостей. Фоновый опрос живёт в `internal/worker/positions`,
поход в Push Service — в `internal/gateway/push`. `internal/container` вручную
связывает реализации, `main` загружает конфигурацию и управляет запуском и
остановкой.

## Запуск

Нужны Go **1.25.7+** и установленный [`tripgoctl`](https://github.com/course-go-autumn-2026/course-infra).
Окружение ЛР3 поднимает PostgreSQL, стек observability и заглушку Push Service.
Из корня репозитория выполните:

```bash
tripgoctl cluster start          # один раз на машине
tripgoctl environment start
tripgoctl connect                # проверить адреса окружения
make migrate
make run
```

`tripgoctl environment start` создаёт `.env` с адресами БД, коллектора и Push
Service (`PUSH_HTTP_URL`, `PUSH_ADMIN_URL`). `make run` сначала
загружает безопасные значения из `.env.example`, затем значения из `.env`.
Пароли и локальные файлы `.env` и `.tripgo/` в Git не попадают. При отсутствии
обязательной переменной сервис завершается с ошибкой. Для остановки отправьте
`SIGINT` (`Ctrl+C`) или `SIGTERM`.

После запуска проверьте:

```bash
curl -i localhost:8080/health
curl -i localhost:8080/ready
```

Адрес HTTP берите из `HTTP_ADDR` в `.env` или `.env.example`, если он переопределён.
`/health` отвечает `200`, пока работает процесс, и не обращается к БД. `/ready`
проверяет PostgreSQL и отвечает `503`, если соединиться не удалось.

## Переменные окружения

Полный пример — [`.env.example`](.env.example). Все значения ниже задаются через
окружение; указаны значения примера, а `DATABASE_URL` берите из `.env`, созданного
`tripgoctl`.

| Переменная | Назначение | Пример |
|---|---|---|
| `HTTP_ADDR` | адрес HTTP-сервера | `:8080` |
| `HTTP_READ_TIMEOUT` | чтение запроса | `15s` |
| `HTTP_READ_HEADER_TIMEOUT` | чтение заголовков | `5s` |
| `HTTP_WRITE_TIMEOUT` | запись ответа | `15s` |
| `HTTP_IDLE_TIMEOUT` | простой соединения | `60s` |
| `LOG_LEVEL` | уровень JSON-логов | `info` |
| `SHUTDOWN_TIMEOUT` | общий бюджет остановки | `10s` |
| `DATABASE_URL` | адрес и учётные данные PostgreSQL | из `.env` |
| `DATABASE_MAX_CONNS` | максимум соединений пула | `10` |
| `DATABASE_MIN_CONNS` | минимум соединений пула | `2` |
| `DATABASE_MAX_CONN_LIFETIME` | срок жизни соединения | `30m` |
| `DATABASE_CONNECT_TIMEOUT` | подключение и стартовый `Ping` | `5s` |
| `DATABASE_QUERY_TIMEOUT` | запросы и транзакции | `3s` |
| `POSITION_CHECK_INTERVAL` | период сканера устаревших поездок и пауза между повторными пушами | `30s` |
| `POSITION_STALE_AFTER` | через сколько без координат поездка считается устаревшей | `60s` |
| `POSITION_WORKERS` | число воркеров опроса | `4` |
| `POSITION_QUEUE_SIZE` | ёмкость очереди опроса | `100` |
| `POSITION_SCAN_BATCH_SIZE` | сколько поездок сканер читает одним запросом | `100` |
| `PUSH_HTTP_URL` | адрес Push Service | из `.env` |
| `PUSH_REQUEST_TIMEOUT` | таймаут одного запроса в Push Service | `1s` |
| `OTEL_SERVICE_NAME` | имя сервиса в телеметрии | `trip-service` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | адрес коллектора | из `.env` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | протокол экспорта | `http/protobuf` или `grpc` |
| `OTEL_TRACES_SAMPLER` | правило сэмплирования | `parentbased_always_on` |
| `OTEL_METRIC_EXPORT_INTERVAL` | интервал экспорта метрик в миллисекундах | `60000` |

При пустом `OTEL_EXPORTER_OTLP_ENDPOINT` сервис запускается с локальным
логированием.

## Команды и проверка

```bash
go build ./...
make generate
make migrate
make migrate-down
make lint
make test
```

`make test` запускает unit-тесты с `-race` и печатает покрытие по пакетам и по
проекту. Порог ЛР2 для каждого пакета домена и usecase составляет 50%.
`make generate` обновляет OpenAPI-код и `gomock`-моки рядом с контрактами.
`make lint` использует [golangci-lint v2.15.0](https://github.com/golangci/golangci-lint/releases/tag/v2.15.0)
из `PATH` и проверяет границы слоёв, форматирование и код с тегом `integration`.

Для интеграционных тестов создайте **отдельную тестовую БД** на PostgreSQL из
окружения `tripgoctl`. Экспортируйте `TEST_DATABASE_URL` с её адресом, примените
текущие миграции и запустите проверки.

```bash
go tool goose -dir migrations postgres "$TEST_DATABASE_URL" up
make test-integration
make test TEST_FLAGS=-count=2
make test-integration TEST_FLAGS=-count=2
```

`make test-integration` требует `TEST_DATABASE_URL` и запускает тесты с
`-race -tags=integration`. Тесты создают и удаляют собственные данные, включая
временные триггеры для проверки отката. Проверка плана запроса опроса вставляет
20 000 поездок внутри транзакции и откатывает её.

Для ручной проверки отправьте `POST /api/v1/trips` с телом из
[условия ЛР1](docs/course/homework/tasks/01-http-and-postgres/README.md), затем запросите
`GET /api/v1/trips/{tripId}` и `POST /api/v1/trips/{tripId}/finish`.
Повторное создание поездки для занятого водителя возвращает `409 driver_busy`,
повторное завершение — `409 trip_completed`.

Координату сохраняет `POST /api/v1/trips/{tripId}/positions`, маршрут отдаёт
`GET /api/v1/trips/{tripId}/positions`:

```bash
curl -i -X POST localhost:8080/api/v1/trips/$TRIP_ID/positions \
  -H 'content-type: application/json' \
  -d "{\"coordinates\":{\"latitude\":59.935,\"longitude\":30.335},\"recorded_at\":\"$(date -u +%FT%TZ)\"}"
curl -i localhost:8080/api/v1/trips/$TRIP_ID/positions
```

Сохранение отвечает `201`, `400 invalid_request` для координат вне диапазона,
неразобранного `recorded_at` или `recorded_at` позже текущего времени больше чем
на минуту, `404 trip_not_found` и `409 trip_completed`. Маршрут отсортирован по
`recorded_at`, затем по `id`; пустой маршрут — `{"positions":[]}`.

### Проверка телеметрии

1. Откройте Jaeger и Grafana по адресам из `tripgoctl connect`. Создайте поездку
   и скопируйте `trace_id` из строки `request handled` для ответа `201`. В Jaeger
   откройте `/trace/<trace_id>`. Под `POST /api/v1/trips` видны спаны PostgreSQL,
   включая вставки в `trips` и `trip_status_history`.
2. В Grafana откройте Explore и выберите Prometheus. Гистограмма HTTP доступна
   как `http_server_request_duration_seconds_bucket`, счётчик создания — как
   `tripgo_trips_created_total`. По умолчанию SDK экспортирует метрики раз в минуту.
3. Для проверки отказа временно масштабируйте только deployment коллектора до
   нуля в namespace текущего окружения в контексте `kind-tripgo-local`. Проверьте
   `/ready` и запрос к поездкам, затем восстановите исходное число реплик
   коллектора.
4. Метрики опроса описаны в разделе «Опрос устаревших поездок». В Jaeger у
   обхода сканера свой трейс `positions.scan`, у каждой обработки задачи —
   отдельный трейс `positions.request` со ссылкой (span link) на обход и
   дочерним клиентским спаном вызова Push Service.

## Идемпотентное создание

Необязательный заголовок `Idempotency-Key` должен содержать UUID, созданный
клиентом. Первый `POST /api/v1/trips` с ключом возвращает `201`, повтор с теми же
данными — `200`, исходное тело ответа и тот же `Location`. Повтор с другим
содержимым возвращает `409 idempotency_conflict`. Без ключа создание работает
как обычно.

Ключ действует **24 часа** после успешного создания. Сравниваются проверенные
значения полей запроса, поэтому порядок полей и пробелы в JSON не влияют на
результат. По истечении срока ключ можно использовать заново; запись с этим
ключом удаляется при следующем обращении. Для очистки остальных просроченных
записей можно периодически выполнять:

```sql
DELETE FROM trip_idempotency_keys WHERE expires_at <= now();
```

Запросы с одним ключом последовательно проходят через транзакционную блокировку
PostgreSQL. Поездка, история статуса и снимок первого ответа сохраняются в одной
транзакции. Миграция `00003` создаёт таблицу ключей; `make migrate-down` откатывает
по одной миграции за вызов.

## Опрос устаревших поездок

Пока поездка активна, устройство водителя шлёт координаты. Если по поездке
давно не было координат, сервис сам просит устройство прислать её через Push
Service (`kind = REQUEST_POSITION`, получатель — водитель поездки, в `data` —
`trip_id`). Горутины на поездку нет: опрос делают один сканер, ограниченная
очередь и пул воркеров фиксированного размера.

```text
                  каждые POSITION_CHECK_INTERVAL
PostgreSQL ──────────────────────────────► сканер (1 горутина, time.Ticker)
  активные поездки, молчащие               │ батчи по POSITION_SCAN_BATCH_SIZE,
  дольше POSITION_STALE_AFTER              │ самые давние первыми
                                           ▼
                        очередь: POSITION_QUEUE_SIZE мест, дедупликация,
                        приоритет «дольше молчит — раньше опросим»
                             ▲                   │
   POST .../positions ───────┘ снимает поездку   │ забирают
   (после коммита)               из очереди      ▼
                                 воркеры: POSITION_WORKERS горутин
                                           │ один запрос, PUSH_REQUEST_TIMEOUT
                                           ▼
                                 Push Service: POST /api/v1/push
```

**Выборка.** Поездки берутся прямо из БД, в памяти их состояние не хранится.
Условие по времени считается в SQL:

```sql
SELECT id, driver_id, COALESCE(last_position_at, started_at) AS last_seen_at
FROM trips
WHERE status = 'active'
  AND COALESCE(last_position_at, started_at) < now() - $1::interval
  AND (COALESCE(last_position_at, started_at), id) > ($2, $3)  -- со второго батча
ORDER BY COALESCE(last_position_at, started_at), id
LIMIT $4
```

Если координат не было ни одной, отсчёт идёт от `started_at`. Под запрос
миграция `00005` создаёт частичный индекс
`trips_active_last_seen_idx ON trips ((COALESCE(last_position_at, started_at)), id) WHERE status = 'active'`
(`CONCURRENTLY`, вне транзакции). На 200 000 поездок, из которых 10 000 активных,
план такой:

```text
Limit (actual rows=100 loops=1)
  ->  Index Scan using trips_active_last_seen_idx on trips (actual rows=100 loops=1)
        Index Cond: ((COALESCE(last_position_at, started_at) < (now() - '00:01:00'::interval))
                     AND (ROW(COALESCE(last_position_at, started_at), id) > ROW(...)))
Execution Time: 0.090 ms
```

Курсор следующего батча попадает в условие индекса, отдельной сортировки и
`OFFSET` нет. Интеграционный тест `TestListStaleUsesIndex` проверяет, что оба
вида запроса читают этот индекс.

**Сканер.** Одна горутина с тикером. Обход идёт внутри неё, поэтому тики не
накладываются: если обход не успел к следующему тику, `time.Ticker` этот тик
пропускает. За тик сканер читает батчи, пока они не кончатся, или
останавливается на батче, в котором переполнилась очередь: следующие строки
только свежее и тоже не попали бы. Следующий тик начинает сначала. Постановка в
очередь не блокирует, поэтому медленный Push Service не останавливает проверку
остальных поездок.

**Очередь.** Ограниченная приоритетная очередь (куча по времени последней
координаты) и множество «сейчас в очереди» под одним мьютексом:

- проверка дубля и вставка идут под одной блокировкой, поэтому одна поездка не
  попадёт в очередь дважды, даже если следующий тик пришёл раньше, чем воркеры
  разобрали предыдущий;
- поездка остаётся в множестве, пока воркер её не обработает: пока запрос по
  ней идёт, повторная постановка даёт `duplicate`;
- координата, сохранённая по ждущей поездке, убирает её из очереди, и запрос
  не уходит. Запрос, который уже отправлен, отменить нельзя, но вторым он не
  станет;
- первой берётся поездка, которая молчит дольше всех.

**Что происходит, когда очередь заполнена.** Пришедшая поездка сравнивается с
самой свежей в очереди. Если она молчит дольше, то вытесняет её; иначе
отбрасывается сама. Отброшенные и вытесненные поездки увеличивают
`tripgo.position_requests{result="dropped"}`, отброшенные ещё и
`tripgo.stale_checks{result="dropped"}`; за тик пишется одна строка `Warn`
`position request queue is full` с числом отброшенных и вытесненных. Потерянных
поездок нет: отметка об отправке не хранится, поэтому они снова попадут в
выборку на следующем тике. Память не растёт: очередь не больше
`POSITION_QUEUE_SIZE`, а сканер держит в памяти один батч.

**Воркеры.** `POSITION_WORKERS` горутин стартуют вместе с сервисом и живут до
остановки; горутина на задачу не создаётся. Каждая задача обрабатывается с
таймаутом `PUSH_REQUEST_TIMEOUT`, одна попытка без ретраев. Ошибка или паника
вызова логируется (`Warn`), учитывается как `result="error"`, и воркер берёт
следующую задачу.

**Пауза между пушами.** Отметку о том, что команду уже слали, сервис не хранит.
Если устройство не ответило, поездка попадёт в выборку на следующем тике, так
что повторный запрос уходит не чаще раза за `POSITION_CHECK_INTERVAL`. Поэтому
период тикера нельзя ставить в секунду. В проде хранили бы время последней
отправки и разводили эти интервалы.

**Гейтвей.** Свой `http.Client` с общим таймаутом и таймаутами подключения,
TLS и ожидания заголовков; `http.DefaultClient` не используется. Тело ответа
всегда вычитывается и закрывается. Ответ, отличный от `202`, — это
`*push.StatusError` с кодом; ошибка транспорта возвращается обёрнутой и
отличается от него через `errors.As`.

**Остановка.** Порядок зафиксирован в `cmd/trip-service/main.go`, всё в пределах
общего `SHUTDOWN_TIMEOUT`:

1. HTTP-сервер перестаёт принимать запросы и дожидается текущих;
2. сканер останавливается — новые задачи не появляются;
3. воркеры дорабатывают то, что уже в очереди. Если бюджет кончился, текущие
   запросы отменяются, а в логе и ошибке остановки видно, сколько задач брошено;
4. закрываются HTTP-клиент Push Service, телеметрия и пул БД.

Если остановить в обратном порядке, закрытый пул БД оборвал бы сканер на
середине, а воркеры потеряли бы задачи из очереди. Сигнал не отменяет воркеров
сам по себе — их останавливает только шаг 3.

**Метрики.**

| Метрика | Тип | Что показывает |
|---|---|---|
| `tripgo.positions.saved` | counter | сохранённые координаты |
| `tripgo.stale_checks{result}` | counter | `queued`, `duplicate`, `dropped` для каждой поездки из выборки |
| `tripgo.stale_checks.rows` | histogram | сколько строк сканер прочитал за тик |
| `tripgo.position_requests.queue.size` | gauge | сколько задач ждёт в очереди |
| `tripgo.position_requests.workers.active` | gauge | сколько воркеров заняты |
| `tripgo.position_requests{result}` | counter | `success`, `error`, `dropped` |
| `tripgo.position_requests.duration{result}` | histogram, с | сколько занял вызов Push Service |

В Prometheus точки заменяются на подчёркивания, к счётчикам добавляется
`_total`, к гистограмме в секундах — `_seconds`: например,
`tripgo_position_requests_total` и `tripgo_position_requests_queue_size`.
Если `workers.active` упёрся в `POSITION_WORKERS`, а `queue.size` растёт, Push
Service отвечает медленнее, чем воркеры успевают разбирать задачи.

**Проверка вручную.** После `tripgoctl environment start`, `make migrate` и
`make run`:

1. создайте поездку и не шлите координаты. Через `POSITION_STALE_AFTER` в
   `tripgoctl environment logs push-service` появится `REQUEST_POSITION`, не
   чаще раза за `POSITION_CHECK_INTERVAL`;
2. шлите координаты каждые 5 секунд (команда в
   [условии ЛР3](docs/course/homework/tasks/03-positions-and-worker-pool/README.md#8-как-проверить-себя)) —
   команд по этой поездке не будет;
3. создайте 200 поездок при `POSITION_QUEUE_SIZE=10`: растёт `dropped`, сервис
   отвечает;
4. задайте заглушке задержку и верните её после проверки:

   ```bash
   curl -fsS -X POST "$PUSH_ADMIN_URL/admin/behaviour" -H 'content-type: application/json' -d '{"latency_ms":5000}'
   curl -fsS -X POST "$PUSH_ADMIN_URL/admin/behaviour" -H 'content-type: application/json' -d '{"latency_ms":0}'
   ```

   Сканер продолжает тикать, HTTP-ручки отвечают быстро;
5. перезапустите сервис с активными поездками — опрос продолжится сам;
6. отправьте `SIGTERM` под нагрузкой — процесс выйдет в пределах
   `SHUTDOWN_TIMEOUT`.

**Тесты.** Unit-тесты `internal/worker/positions` покрывают все сценарии
условия: повторный тик не ставит поездку дважды, координата между постановкой
и обработкой отменяет отправку, переполнение даёт `dropped` без блокировки
сканера, N задач обрабатываются ровно `POSITION_WORKERS` воркерами, отмена
контекста завершает сканер и воркеров, ошибка Push Service не роняет воркер.
Тикер спрятан за интерфейсом и в тестах управляется вручную, `time.Sleep` не
используется, `goleak.VerifyTestMain` подключён в пакетах воркера и гейтвея.
Интеграционные тесты проверяют выборку, батчи, план запроса и атомарное
сохранение координаты с откатом.

## Запуск в Docker

`Dockerfile` собирает бинарь в образе Go и переносит его в
`distroless/static-debian12:nonroot`. В итоговом образе нет исходников, тестов и
Go toolchain; процесс работает с UID/GID `65532:65532`. Размер образа при
проверке для `linux/arm64` — **4 267 012 байт** (около 4,1 МиБ).

Пример отдельного локального запуска сервиса рядом с PostgreSQL в Docker:

```bash
docker build -t trip-service:lab1 .
docker network create trip-lab
docker run -d --name trip-postgres --network trip-lab -p 55432:5432 \
  -e POSTGRES_USER=tripgo -e POSTGRES_PASSWORD=tripgo -e POSTGRES_DB=tripgo \
  postgres:17-alpine
until docker exec trip-postgres pg_isready -U tripgo -d tripgo; do sleep 1; done

docker_db_url='postgres://tripgo:tripgo@127.0.0.1:55432/tripgo?sslmode=disable'
go tool goose -dir migrations postgres "$docker_db_url" up
docker run --rm --name trip-service --network trip-lab -p 8080:8080 \
  --env-file .env.example \
  -e DATABASE_URL='postgres://tripgo:tripgo@trip-postgres:5432/tripgo?sslmode=disable' \
  trip-service:lab1
```

Во втором терминале проверьте `curl -i localhost:8080/ready`; ожидается `200`.
После остановки сервиса удалите проверочное окружение командами
`docker rm -f trip-postgres` и `docker network rm trip-lab`.

## Решения

- Транзакции открывает `TxManager.Do`. Он помещает `pgx.Tx` в контекст, при
  успехе выполняет `COMMIT`, при ошибке или панике — `ROLLBACK`. Репозиторий
  выбирает транзакцию из контекста, а вне `Do` использует пул. Вложенный `Do`
  переиспользует текущую транзакцию. Создание поездки и запись истории статуса
  выполняются в одном `Do`; завершение и его запись в историю — тоже.
- Уровень изоляции — **Read Committed**. Этого достаточно для этих операций:
  частичный уникальный индекс по `driver_id` запрещает две активные поездки
  даже при одновременных вставках, а завершение обновляет только строку со
  статусом `active`. Второй конкурентный вызов видит уже завершённую поездку.
- Нарушение уникального индекса распознаётся по коду PostgreSQL `23505` и
  превращается в `driver_busy`. SQL в репозитории собирается через `squirrel`
  с плейсхолдерами `$N`.
- HTTP-типы и серверный интерфейс генерируются из неизменённого OpenAPI.
  Выбраны семь операций: `createTrip`, `getTrip`, `finishTrip`,
  `createTripPosition`, `listTripPositions`, `health`, `ready`. Ошибки имеют
  формат `application/problem+json`.
- Сохранение координаты блокирует строку поездки (`SELECT … FOR UPDATE`),
  проверяет, что она активна, вставляет точку и обновляет `last_position_at` в
  одной транзакции через `TxManager.Do`. Обновление —
  `last_position_at = GREATEST(last_position_at, $1)`: сравнение делается в самом
  запросе, поэтому координата, присланная из офлайна позже свежей, не
  «состарит» поездку. Блокировка строки исключает гонку с завершением поездки.
- Поездка убирается из очереди опроса только после коммита: откатившаяся
  координата не отменяет запрос.

Сервис реализует обязательные части ЛР1–ЛР3 и задания со звёздочкой:
идемпотентное создание и Docker-образ ЛР1, мутационное тестирование ЛР2
([`docs/lab2/mutation-testing`](docs/lab2/mutation-testing/README.md)), выборку
батчами и приоритетную очередь ЛР3.
