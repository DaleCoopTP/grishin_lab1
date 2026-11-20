# Trip Service

Сервис создаёт поездки, возвращает их по ID и завершает, принимает координаты
и отдаёт маршрут. Если от поездки давно не было координат, сервис сам просит
устройство водителя прислать координату через Push Service. HTTP API задан в
[`contracts/openapi/trip-service.openapi.yaml`](contracts/openapi/trip-service.openapi.yaml),
данные хранятся в PostgreSQL. Архитектура описана в
[`docs/architecture.md`](docs/architecture.md), материалы курса лежат в
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

Все лабораторные по очереди развивают один и тот же сервис. Копия контрактов в
`docs/course/homework/contracts/` относится к материалам курса, а код сервиса
генерируется из корневого `contracts/`.

## Архитектура

```text
HTTP-хендлер → usecase → домен
                  │
                  └─ контракт репозитория ← PostgreSQL-репозиторий

worker/positions (сканер → очередь → воркеры) → gateway/push → Push Service
```

Создание, чтение и завершение поездки, сохранение координаты и получение
маршрута разнесены по своим пакетам на слоях HTTP и usecase. HTTP разбирает
запросы и превращает ошибки в ответы через `http/domainerror`. Usecase управляет
транзакциями и вызывает доменные правила, домен проверяет поездку и переход
статуса. Репозиторий выполняет SQL через пул или через транзакцию из контекста.

Интерфейсы лежат в `contract.go` у потребителя: хендлер описывает нужный ему
usecase, usecase описывает нужные ему операции репозитория и менеджера
транзакций. Поэтому каждый пакет зависит только от того, что вызывает, и в
тестах эти зависимости легко подменить. Фоновый опрос находится в
`internal/worker/positions`, клиент Push Service в `internal/gateway/push`.
`internal/container` вручную связывает реализации, а `main` загружает
конфигурацию, запускает и останавливает сервис.

## Запуск

Нужны Go 1.25.7+ и установленный [`tripgoctl`](https://github.com/course-go-autumn-2026/course-infra).
Окружение ЛР3 поднимает PostgreSQL, стек observability и заглушку Push Service.
Из корня репозитория выполните:

```bash
tripgoctl cluster start          # один раз на машине
tripgoctl environment start
tripgoctl connect                # проверить адреса окружения
make migrate
make run
```

`tripgoctl environment start` записывает в `.env` адреса БД, коллектора и Push
Service (`PUSH_HTTP_URL`). `make run` сначала загружает безопасные значения из
`.env.example`, потом значения из `.env`. Пароли и локальные файлы `.env` и
`.tripgo/` в Git не попадают. Если обязательной переменной нет, сервис
завершается с ошибкой. Остановить его можно через `SIGINT` (`Ctrl+C`) или
`SIGTERM`.

После запуска проверьте:

```bash
curl -i localhost:8080/health
curl -i localhost:8080/ready
```

Если `HTTP_ADDR` переопределён, берите адрес из `.env` или `.env.example`.
`/health` отвечает `200`, пока процесс работает, и в БД не ходит. `/ready`
проверяет PostgreSQL и отвечает `503`, если соединиться не удалось.

## Переменные окружения

Полный пример лежит в [`.env.example`](.env.example). Все значения ниже задаются
через окружение. В таблице указаны значения из примера; `DATABASE_URL` берите из
`.env`, который создал `tripgoctl`.

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

С пустым `OTEL_EXPORTER_OTLP_ENDPOINT` сервис работает и пишет только локальные
логи.

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
всему проекту. По требованию ЛР2 у каждого пакета домена и usecase покрытие
должно быть не ниже 50%. `make generate` обновляет OpenAPI-код и `gomock`-моки
рядом с контрактами. `make lint` берёт [golangci-lint v2.15.0](https://github.com/golangci/golangci-lint/releases/tag/v2.15.0)
из `PATH` и проверяет границы слоёв, форматирование и код с тегом `integration`.

Для интеграционных тестов создайте отдельную тестовую БД на PostgreSQL из
окружения `tripgoctl`, экспортируйте её адрес в `TEST_DATABASE_URL`, накатите
миграции и запустите проверки:

```bash
go tool goose -dir migrations postgres "$TEST_DATABASE_URL" up
make test-integration
make test TEST_FLAGS=-count=2
make test-integration TEST_FLAGS=-count=2
```

`make test-integration` без `TEST_DATABASE_URL` не запустится; тесты идут с
`-race -tags=integration`. Они сами создают и удаляют свои данные, в том числе
временные триггеры для проверки отката. Проверка плана запроса опроса вставляет
20 000 поездок внутри транзакции и потом откатывает её.

Для ручной проверки отправьте `POST /api/v1/trips` с телом из
[условия ЛР1](docs/course/homework/tasks/01-http-and-postgres/README.md), затем
запросите `GET /api/v1/trips/{tripId}` и `POST /api/v1/trips/{tripId}/finish`.
Повторное создание поездки для занятого водителя вернёт `409 driver_busy`,
повторное завершение вернёт `409 trip_completed`.

Координату сохраняет `POST /api/v1/trips/{tripId}/positions`, маршрут отдаёт
`GET /api/v1/trips/{tripId}/positions`:

```bash
curl -i -X POST localhost:8080/api/v1/trips/$TRIP_ID/positions \
  -H 'content-type: application/json' \
  -d "{\"coordinates\":{\"latitude\":59.935,\"longitude\":30.335},\"recorded_at\":\"$(date -u +%FT%TZ)\"}"
curl -i localhost:8080/api/v1/trips/$TRIP_ID/positions
```

Сохранение отвечает `201`. `400 invalid_request` приходит, если координаты вне
диапазона, `recorded_at` не разобрался или опережает текущее время больше чем на
минуту. Ещё возможны `404 trip_not_found` и `409 trip_completed`. Маршрут
отсортирован по `recorded_at`, затем по `id`, а пустой маршрут выглядит как
`{"positions":[]}`.

### Проверка телеметрии

1. Откройте Jaeger и Grafana по адресам из `tripgoctl connect`. Создайте поездку
   и скопируйте `trace_id` из строки `request handled` для ответа `201`. В Jaeger
   откройте `/trace/<trace_id>`: под `POST /api/v1/trips` видны спаны PostgreSQL,
   включая вставки в `trips` и `trip_status_history`.
2. В Grafana откройте Explore и выберите Prometheus. Гистограмма HTTP называется
   `http_server_request_duration_seconds_bucket`, счётчик созданных поездок
   `tripgo_trips_created_total`. По умолчанию SDK отправляет метрики раз в минуту.
3. Чтобы проверить отказ коллектора, временно масштабируйте только его
   deployment до нуля в namespace текущего окружения в контексте
   `kind-tripgo-local`. Проверьте `/ready` и запрос к поездкам, потом верните
   прежнее число реплик.
4. Метрики опроса перечислены в разделе «Опрос устаревших поездок». В Jaeger у
   каждого обхода сканера свой трейс `positions.scan`. Каждая обработка задачи
   идёт отдельным трейсом `positions.request` со ссылкой (span link) на обход, а
   вызов Push Service виден в нём как дочерний клиентский спан.

## Идемпотентное создание

Необязательный заголовок `Idempotency-Key` должен содержать UUID, который
сгенерировал клиент. Первый `POST /api/v1/trips` с ключом возвращает `201`.
Повтор с теми же данными возвращает `200`, исходное тело ответа и тот же
`Location`, повтор с другим содержимым возвращает `409 idempotency_conflict`. Без
ключа создание работает как обычно.

Ключ действует 24 часа после успешного создания. Сравниваются уже проверенные
значения полей запроса, так что порядок полей и пробелы в JSON на результат не
влияют. Просроченный ключ можно использовать снова, а его старая запись
удаляется при следующем обращении. Остальные просроченные записи можно
периодически чистить так:

```sql
DELETE FROM trip_idempotency_keys WHERE expires_at <= now();
```

Запросы с одним ключом выполняются по очереди под транзакционной блокировкой
PostgreSQL. Поездка, история статуса и снимок первого ответа сохраняются в одной
транзакции. Таблицу ключей создаёт миграция `00003`; `make migrate-down`
откатывает по одной миграции за вызов.

## Опрос устаревших поездок

Пока поездка активна, устройство водителя присылает координаты. Если от поездки
давно ничего не было, сервис отправляет в Push Service команду
`kind = REQUEST_POSITION`. Получатель команды — водитель поездки, в `data`
передаётся `trip_id`. Горутина на каждую поездку не заводится: опросом
занимаются один сканер, ограниченная очередь и пул воркеров фиксированного
размера.

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

### Выборка

Сканер берёт поездки прямо из БД и не хранит их состояние в памяти. Условие по
времени считается в SQL:

```sql
SELECT id, driver_id, COALESCE(last_position_at, started_at) AS last_seen_at
FROM trips
WHERE status = 'active'
  AND COALESCE(last_position_at, started_at) < now() - $1::interval
  AND (COALESCE(last_position_at, started_at), id) > ($2, $3)  -- со второго батча
ORDER BY COALESCE(last_position_at, started_at), id
LIMIT $4
```

Если координат по поездке ещё не было, время отсчитывается от `started_at`. Для
этого запроса миграция `00005` создаёт частичный индекс
`trips_active_last_seen_idx ON trips ((COALESCE(last_position_at, started_at)), id) WHERE status = 'active'`
(через `CONCURRENTLY`, вне транзакции). На 200 000 поездок, из которых 10 000
активных, план получается такой:

```text
Limit (actual rows=100 loops=1)
  ->  Index Scan using trips_active_last_seen_idx on trips (actual rows=100 loops=1)
        Index Cond: ((COALESCE(last_position_at, started_at) < (now() - '00:01:00'::interval))
                     AND (ROW(COALESCE(last_position_at, started_at), id) > ROW(...)))
Execution Time: 0.090 ms
```

Курсор следующего батча входит в условие индекса, поэтому `OFFSET` и отдельная
сортировка не нужны. Интеграционный тест `TestListStaleUsesIndex` проверяет, что
и первый батч, и продолжение читают этот индекс.

### Сканер

Сканер работает в одной горутине с тикером, и обход идёт в ней же. Поэтому тики
не накладываются: если обход не уложился до следующего тика, `time.Ticker`
просто пропустит этот тик. За один тик сканер читает батчи до конца или до
батча, на котором переполнилась очередь. Строки после него только свежее и в
очередь тоже не попали бы, а следующий тик всё равно начнёт сначала. Постановка
в очередь не блокируется, так что медленный Push Service не мешает проверять
остальные поездки.

### Очередь

Очередь ограничена и упорядочена по приоритету: это куча по времени последней
координаты плюс множество поездок «сейчас в очереди», и то и другое под одним
мьютексом.

- Проверка дубля и вставка идут под одной блокировкой, поэтому поездка не
  попадёт в очередь дважды, даже если новый тик пришёл раньше, чем воркеры
  разобрали предыдущий.
- Поездка остаётся в множестве, пока воркер её не обработает. Пока запрос по ней
  идёт, повторная постановка даёт `duplicate`.
- Если по ждущей поездке сохранилась координата, поездка удаляется из очереди и
  запрос не уходит. Уже отправленный запрос не отменяется, но второго по той же
  поездке до его завершения не будет.
- Первой из очереди берётся поездка, которая молчит дольше всех.

### Что происходит, когда очередь заполнена

Новая поездка сравнивается с самой свежей из стоящих в очереди. Если новая
молчит дольше, она занимает её место, иначе отбрасывается сама. Каждая
отброшенная или вытесненная поездка увеличивает
`tripgo.position_requests{result="dropped"}`, отброшенные ещё и
`tripgo.stale_checks{result="dropped"}`. За тик в лог пишется одна строка `Warn`
`position request queue is full` с числом отброшенных и вытесненных поездок.

Такие поездки не теряются. Отметка об отправке нигде не хранится, поэтому на
следующем тике они снова попадут в выборку. Память при этом не растёт: в очереди
не больше `POSITION_QUEUE_SIZE` задач, а сканер держит в памяти один батч.

### Воркеры

`POSITION_WORKERS` горутин стартуют вместе с сервисом и работают до остановки,
отдельная горутина на задачу не создаётся. На каждую задачу даётся
`PUSH_REQUEST_TIMEOUT` и одна попытка, ретраев нет. Если вызов вернул ошибку или
запаниковал, воркер пишет `Warn`, учитывает результат как `result="error"` и
берёт следующую задачу.

### Пауза между пушами

Сервис не запоминает, что команду уже отправляли. Если устройство не ответило,
поездка попадёт в выборку на следующем тике, поэтому повторный запрос уходит не
чаще раза в `POSITION_CHECK_INTERVAL`. Из-за этого период тикера нельзя ставить
в секунду. В проде хранили бы время последней отправки и задавали бы два
интервала отдельно.

### Гейтвей

У клиента свой `http.Client` с общим таймаутом и отдельными таймаутами на
подключение, TLS и ожидание заголовков; `http.DefaultClient` не используется.
Тело ответа всегда дочитывается и закрывается. На любой ответ кроме `202`
клиент возвращает `*push.StatusError` с кодом, а ошибку транспорта оборачивает,
и через `errors.As` их можно различить.

### Остановка

Порядок остановки задан в `cmd/trip-service/main.go`, и всё укладывается в общий
`SHUTDOWN_TIMEOUT`:

1. HTTP-сервер перестаёт принимать запросы и ждёт текущие;
2. сканер останавливается, и новые задачи больше не появляются;
3. воркеры дорабатывают то, что уже стоит в очереди. Если бюджет кончился,
   текущие запросы отменяются, а лог и ошибка остановки показывают, сколько
   задач осталось необработанными;
4. закрываются HTTP-клиент Push Service, телеметрия и пул БД.

В обратном порядке закрытый пул БД оборвал бы сканер посреди обхода, а воркеры
потеряли бы задачи из очереди. Сам сигнал воркеров не прерывает, они
останавливаются только на шаге 3.

### Метрики

| Метрика | Тип | Что показывает |
|---|---|---|
| `tripgo.positions.saved` | counter | сохранённые координаты |
| `tripgo.stale_checks{result}` | counter | `queued`, `duplicate`, `dropped` для каждой поездки из выборки |
| `tripgo.stale_checks.rows` | histogram | сколько строк сканер прочитал за тик |
| `tripgo.position_requests.queue.size` | gauge | сколько задач ждёт в очереди |
| `tripgo.position_requests.workers.active` | gauge | сколько воркеров заняты |
| `tripgo.position_requests{result}` | counter | `success`, `error`, `dropped` |
| `tripgo.position_requests.duration{result}` | histogram, с | сколько занял вызов Push Service |

В Prometheus метрики попадают через OTel Collector (`job="otel-collector"`,
`exported_job="tripgo/trip-service"`) под такими именами:
`tripgo_positions_saved_total`, `tripgo_stale_checks_total{result}`,
`tripgo_stale_checks_rows_bucket|_sum|_count`,
`tripgo_position_requests_queue_size`, `tripgo_position_requests_workers_active`,
`tripgo_position_requests_total{result}`,
`tripgo_position_requests_duration_seconds_bucket|_sum|_count`. Примеры запросов:

```promql
max(tripgo_position_requests_queue_size)
max(tripgo_position_requests_workers_active)
sum by (result) (rate(tripgo_position_requests_total[1m]))
histogram_quantile(0.95, sum by (le) (rate(tripgo_position_requests_duration_seconds_bucket[1m])))
sum(rate(tripgo_stale_checks_rows_sum[1m])) / sum(rate(tripgo_stale_checks_rows_count[1m]))
```

Если `workers.active` упёрся в `POSITION_WORKERS`, а `queue.size` растёт, значит
Push Service отвечает медленнее, чем воркеры успевают разбирать задачи. Дашборд с
этими графиками лежит в [`docs/lab3/grafana-dashboard.json`](docs/lab3/grafana-dashboard.json)
(Grafana → Dashboards → New → Import), снимок под нагрузкой в
[`docs/lab3/grafana-pool.png`](docs/lab3/grafana-pool.png). Гейджи очереди и
воркеров снимаются в момент экспорта метрик. Если `OTEL_METRIC_EXPORT_INTERVAL`
кратен `POSITION_CHECK_INTERVAL`, экспорт каждый раз попадает в одну и ту же
фазу тика и может постоянно показывать пустую очередь.

### Проверка вручную

После `tripgoctl environment start`, `make migrate` и `make run`:

1. создайте поездку и не шлите координаты. Через `POSITION_STALE_AFTER` в
   `tripgoctl environment logs push-service` появится `REQUEST_POSITION`, причём
   не чаще раза в `POSITION_CHECK_INTERVAL`;
2. шлите координаты каждые 5 секунд (команда есть в
   [условии ЛР3](docs/course/homework/tasks/03-positions-and-worker-pool/README.md#8-как-проверить-себя)),
   и команд по этой поездке не будет;
3. создайте 200 поездок при `POSITION_QUEUE_SIZE=10`: `dropped` растёт, а сервис
   продолжает отвечать;
4. задайте заглушке задержку и после проверки верните её. Адрес admin API
   показан в строке `Push admin` вывода `tripgoctl connect`. В `.env` его нет, но
   в окружении ЛР3 он совпадает с `PUSH_HTTP_URL`:

   ```bash
   PUSH_ADMIN_URL=$PUSH_HTTP_URL
   curl -fsS -X POST "$PUSH_ADMIN_URL/admin/behaviour" -H 'content-type: application/json' -d '{"latency_ms":5000}'
   curl -fsS -X POST "$PUSH_ADMIN_URL/admin/behaviour" -H 'content-type: application/json' -d '{"latency_ms":0}'
   ```

   Сканер при этом продолжает тикать, а HTTP-ручки отвечают быстро;
5. перезапустите сервис с активными поездками: опрос продолжится сам;
6. отправьте `SIGTERM` под нагрузкой, и процесс завершится в пределах
   `SHUTDOWN_TIMEOUT`.

### Тесты

Unit-тесты `internal/worker/positions` проходят по всем сценариям из условия.
Повторный тик не ставит поездку в очередь второй раз, а координата, пришедшая
между постановкой и обработкой, отменяет отправку. Переполнение даёт `dropped`
и не блокирует сканер. N задач обрабатываются ровно `POSITION_WORKERS`
воркерами. Отмена контекста завершает сканер и воркеров, ошибка Push Service не
роняет воркер.

Тикер спрятан за интерфейсом, и в тестах его дёргают вручную. `time.Sleep` не
используется, а в пакетах воркера и гейтвея подключён `goleak.VerifyTestMain`.
Интеграционные тесты проверяют выборку, батчи, план запроса и то, что
координата сохраняется атомарно и откатывается при ошибке.

## Запуск в Docker

`Dockerfile` собирает бинарь в образе Go и переносит его в
`distroless/static-debian12:nonroot`. В итоговом образе нет исходников, тестов и
Go toolchain, а процесс работает с UID/GID `65532:65532`. Образ для
`linux/arm64` при проверке весил 4 267 012 байт, около 4,1 МиБ.

Отдельно запустить сервис рядом с PostgreSQL в Docker можно так:

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

Во втором терминале выполните `curl -i localhost:8080/ready`, ответ должен быть
`200`. Когда сервис остановлен, удалите проверочное окружение командами
`docker rm -f trip-postgres` и `docker network rm trip-lab`.

## Решения

- Транзакции открывает `TxManager.Do`. Он кладёт `pgx.Tx` в контекст и при
  успехе делает `COMMIT`, а при ошибке или панике `ROLLBACK`. Репозиторий берёт
  транзакцию из контекста, а вне `Do` работает через пул. Вложенный `Do`
  использует текущую транзакцию. Создание поездки с записью истории статуса идёт
  в одном `Do`, завершение со своей записью в историю тоже.
- Уровень изоляции Read Committed. Для этих операций его хватает: частичный
  уникальный индекс по `driver_id` не даст создать две активные поездки даже при
  одновременных вставках, а завершение обновляет только строку со статусом
  `active`, так что второй конкурентный вызов увидит уже завершённую поездку.
- Нарушение уникального индекса определяется по коду PostgreSQL `23505` и
  превращается в `driver_busy`. SQL в репозитории собирается через `squirrel`
  с плейсхолдерами `$N`.
- HTTP-типы и серверный интерфейс генерируются из неизменённого OpenAPI. Выбраны
  семь операций: `createTrip`, `getTrip`, `finishTrip`, `createTripPosition`,
  `listTripPositions`, `health`, `ready`. Ошибки отдаются в формате
  `application/problem+json`.
- Сохранение координаты в одной транзакции через `TxManager.Do` блокирует строку
  поездки (`SELECT … FOR UPDATE`), проверяет, что поездка активна, вставляет
  точку и обновляет `last_position_at = GREATEST(last_position_at, $1)`.
  Сравнение происходит в самом запросе, поэтому координата из офлайна, пришедшая
  позже свежей, не «состарит» поездку. Блокировка строки исключает гонку с
  завершением поездки.
- Поездка снимается с опроса только после коммита, так что откатившаяся
  координата запрос не отменяет.

Сервис закрывает обязательные части ЛР1, ЛР2 и ЛР3 и задания со звёздочкой:
идемпотентное создание и Docker-образ из ЛР1, мутационное тестирование из ЛР2
([`docs/lab2/mutation-testing`](docs/lab2/mutation-testing/README.md)), выборку
батчами и приоритетную очередь из ЛР3.
