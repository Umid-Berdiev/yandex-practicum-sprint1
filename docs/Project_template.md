# Задание 1. Анализ и планирование

## 1. Изучение функциональности монолитного приложения

Приложение **Smart Home** реализует два основных сценария:

### 1.1 Управление отоплением

Управление статусом устройств отопления осуществляется через поле `status` модели `Sensor` (`active` / `inactive`).  
Эндпоинт `PATCH /api/v1/sensors/:id/value` позволяет удалённо обновить состояние датчика/устройства.

### 1.2 Мониторинг температуры

- Датчики (тип `temperature`) хранятся в PostgreSQL.
- При каждом запросе данные обогащаются актуальными показаниями из внешнего **Temperature API** (`GET /temperature/:id` и `GET /temperature?location=...`).
- Пользователи получают температуру через `GET /api/v1/sensors/temperature/:location`.

---

## 2. Архитектура монолитного приложения

| Характеристика      | Описание                                                      |
| ------------------- | ------------------------------------------------------------- |
| Язык                | Go 1.x                                                        |
| HTTP-фреймворк      | Gin                                                           |
| База данных         | PostgreSQL (через `pgx/v5` connection pool)                   |
| Архитектурный стиль | Монолит — handlers / services / db / models в одном процессе  |
| Взаимодействие      | Синхронное REST API                                           |
| Масштабируемость    | Только горизонтальное масштабирование всего монолита целиком  |
| Развёртывание       | Требует остановки всего приложения (`docker-compose down/up`) |

### Структура кода

```
smart_home/
├── main.go                  ← точка входа, wire-up зависимостей
├── handlers/sensors.go      ← HTTP-обработчики (все домены в одном файле)
├── services/temperature_service.go  ← HTTP-клиент к внешнему Temperature API
├── models/sensor.go         ← единственная модель данных
└── db/db.go                 ← репозиторий (весь SQL в одном месте)
```

### Слабые стороны

- **Единая точка отказа** — ошибка в одном компоненте роняет всё приложение.
- **Синхронная блокировка** — запрос к `/sensors` блокируется на N вызовах к Temperature API (по одному на каждый температурный датчик).
- **Жёсткая связанность** — бизнес-логика нескольких доменов перемешана в одном хендлере.
- **Ограниченная масштабируемость** — нельзя независимо масштабировать горячий контур мониторинга температуры.
- **Деплой** — обновление любого компонента требует остановки всего сервиса.

---

## 3. Домены и ограниченные контексты (DDD)

### Домен: Smart Home

| Bounded Context                                     | Ответственность                                | Ключевые сущности                 |
| --------------------------------------------------- | ---------------------------------------------- | --------------------------------- |
| **Device Management** (Управление Устройствами)     | CRUD датчиков и устройств, хранение метаданных | `Sensor`, `Device`                |
| **Temperature Monitoring** (Мониторинг Температуры) | Сбор и отображение показаний температуры       | `TemperatureReading`, `Sensor`    |
| **Heating Control** (Управление Отоплением)         | Включение/выключение отопительных устройств    | `HeatingDevice`, `HeatingCommand` |

### Взаимодействие между контекстами

```
Device Management  ──(предоставляет устройства)──►  Heating Control
Device Management  ──(предоставляет датчики)──────►  Temperature Monitoring
Temperature Monitoring  ◄──(показания)──  External Temperature API
```

---

## 4. Диаграмма контекста C4 (PlantUML)

Файл: [`docs/c4-context.puml`](c4-context.puml)

```plantuml
@startuml C4_Context_SmartHome
!include https://raw.githubusercontent.com/plantuml-stdlib/C4-PlantUML/master/C4_Context.puml

Person(user, "Пользователь", "Просматривает температуру, управляет отоплением")
System(smarthome, "Smart Home Monolith", "Go + PostgreSQL. REST API.")
System_Ext(temperature_api, "Temperature API", "Внешний сервис показаний температуры")
System_Ext(sensors, "IoT Temperature Sensors", "Физические датчики в домах")
System_Ext(postgres, "PostgreSQL", "Метаданные датчиков")

Rel(user, smarthome, "REST / HTTPS")
Rel(smarthome, temperature_api, "HTTP GET (синхронно)")
Rel(temperature_api, sensors, "IoT / MQTT")
Rel(smarthome, postgres, "TCP / pgx")
@enduml
```

---

# Задание 2. Проектирование микросервисной архитектуры

## 1. Декомпозиция монолита на микросервисы

### As-Is → To-Be

| As-Is (Монолит)                                 | To-Be (Микросервис)                  | Обоснование                                                            |
| ----------------------------------------------- | ------------------------------------ | ---------------------------------------------------------------------- |
| `handlers/sensors.go` — CRUD датчиков           | **Device Management Service**        | Изолированный bounded context управления метаданными устройств         |
| `handlers/sensors.go` — patch value / status    | **Heating Control Service**          | Самостоятельный домен с командной моделью и журналом аудита            |
| `services/temperature_service.go` — polling API | **Temperature Monitoring Service**   | Горячий контур, требует независимого масштабирования и хранилища TS    |
| `main.go` — единая точка входа                  | **API Gateway**                      | Маршрутизация, аутентификация, rate-limiting вынесены в отдельный слой |
| Общая PostgreSQL                                | БД на каждый сервис (DB-per-Service) | Изоляция данных, независимые схемы и миграции                          |
| Синхронные вызовы между доменами                | **Apache Kafka** (брокер событий)    | Асинхронная связь снижает coupling и устраняет каскадные отказы        |

### Новые микросервисы (To-Be)

| Сервис                             | Технологии            | Ответственность                                             |
| ---------------------------------- | --------------------- | ----------------------------------------------------------- |
| **API Gateway**                    | Go / nginx + JWT      | Единая точка входа, аутентификация, маршрутизация           |
| **Device Management Service**      | Go, REST, PostgreSQL  | CRUD устройств/датчиков, публикация device-событий          |
| **Heating Control Service**        | Go, REST, PostgreSQL  | Команды включения/выключения, расписания, журнал команд     |
| **Temperature Monitoring Service** | Go, REST, TimescaleDB | Polling внешнего API, хранение TS-данных, алерты по порогам |

---

## 2. Взаимодействие между сервисами

### Синхронное (REST)

```
Пользователь → API Gateway → [Device / Heating / Monitoring] Service
```

### Асинхронное (Kafka)

| Топик                 | Producer                       | Consumer(s)                               |
| --------------------- | ------------------------------ | ----------------------------------------- |
| `device.created`      | Device Management Service      | Heating Control, Temperature Monitoring   |
| `device.updated`      | Device Management Service      | Heating Control, Temperature Monitoring   |
| `heating.turned_on`   | Heating Control Service        | Device Management (синхронизация статуса) |
| `heating.turned_off`  | Heating Control Service        | Device Management (синхронизация статуса) |
| `temperature.reading` | Temperature Monitoring Service | (будущие: аналитика, нотификации)         |
| `temperature.alert`   | Temperature Monitoring Service | (будущие: Notification Service)           |

### Database-per-Service

| Сервис                         | СУБД        | Назначение                           |
| ------------------------------ | ----------- | ------------------------------------ |
| Device Management Service      | PostgreSQL  | Устройства, типы, местоположения     |
| Heating Control Service        | PostgreSQL  | Состояния устройств, журнал команд   |
| Temperature Monitoring Service | TimescaleDB | Временные ряды показаний температуры |

---

## 3. Диаграммы C4

### 3.1 Уровень контейнеров (C4 L2)

Файл: [`docs/c4-containers.puml`](c4-containers.puml)

Показывает все контейнеры (микросервисы, БД, брокер), пользователей и внешние системы
с указанием протоколов взаимодействия.

### 3.2 Уровень компонентов (C4 L3)

| Файл                                                                  | Сервис                         |
| --------------------------------------------------------------------- | ------------------------------ |
| [`docs/c4-components-device.puml`](c4-components-device.puml)         | Device Management Service      |
| [`docs/c4-components-heating.puml`](c4-components-heating.puml)       | Heating Control Service        |
| [`docs/c4-components-monitoring.puml`](c4-components-monitoring.puml) | Temperature Monitoring Service |

Ключевые компоненты каждого сервиса:

- **REST API Handler** — HTTP-обработчик, валидация входных данных
- **Command Handler** — оркестрация бизнес-операций, применение инвариантов
- **State Manager** — управление жизненным циклом сущности
- **Repository** — инкапсуляция SQL (паттерн Repository)
- **Event Publisher / Consumer** — асинхронная интеграция через Kafka

### 3.3 Уровень кода (C4 L4) — Sequence Diagrams

| Файл                                                                        | Сценарий                                |
| --------------------------------------------------------------------------- | --------------------------------------- |
| [`docs/c4-sequence-turn-on-heating.puml`](c4-sequence-turn-on-heating.puml) | Включение отопления (критичный путь)    |
| [`docs/c4-sequence-temperature.puml`](c4-sequence-temperature.puml)         | Получение температуры + фоновый polling |

#### Критичный сценарий: Включение отопления

```
Пользователь → API Gateway
  → Heating Control Service
    → Heating Repository → Heating DB  (проверка состояния)
    → Heating Repository → Heating DB  (запись команды)
    → Heating Repository → Heating DB  (обновление статуса)
    → Event Publisher → Kafka (heating.turned_on)
  ← 200 OK

[асинхронно]
Kafka → Device Management Service → Device DB  (синхронизация статуса)
```

Ключевое решение: ответ пользователю не ждёт синхронизации в Device Management —
это устраняет связанность и повышает отказоустойчивость.

---

# Задание 3. Разработка ER-диаграммы

## 1. Идентификация сущностей

| Сущность                           | Bounded Context        | Назначение                                           |
| ---------------------------------- | ---------------------- | ---------------------------------------------------- |
| **User** (Пользователь)            | Общий                  | Владелец или гость умного дома                       |
| **House** (Дом)                    | Device Management      | Физический дом, к которому привязаны устройства      |
| **UserHouseAccess**                | Device Management      | Таблица доступа (many-to-many User ↔ House)          |
| **DeviceType** (Тип устройства)    | Device Management      | Справочник типов: датчик, актуатор, термостат        |
| **Device** (Устройство)            | Device Management      | Конкретное физическое устройство в доме              |
| **Module** (Модуль)                | Heating / Monitoring   | Функциональный модуль устройства (отопление, датчик) |
| **HeatingSchedule** (Расписание)   | Heating Control        | Расписание автоматического включения отопления       |
| **HeatingCommand** (Журнал команд) | Heating Control        | Аудит-лог всех команд управления отоплением          |
| **TelemetryData** (Телеметрия)     | Temperature Monitoring | Временной ряд показаний датчиков (TimescaleDB)       |
| **Alert** (Алерт)                  | Temperature Monitoring | Уведомления при выходе показаний за пороги           |

## 2. Атрибуты ключевых сущностей

### Device (Устройство)

| Поле               | Тип                 | Описание                                |
| ------------------ | ------------------- | --------------------------------------- |
| `id`               | UUID PK             | Уникальный идентификатор                |
| `type_id`          | UUID FK→DeviceType  | Тип устройства                          |
| `house_id`         | UUID FK→House       | Дом, которому принадлежит устройство    |
| `name`             | VARCHAR(100)        | Пользовательское название               |
| `serial_number`    | VARCHAR(100) UNIQUE | Серийный номер                          |
| `location`         | VARCHAR(100)        | Комната / зона в доме                   |
| `status`           | ENUM                | active / inactive / error / maintenance |
| `firmware_version` | VARCHAR(50)         | Версия прошивки                         |

### TelemetryData (Телеметрия)

| Поле          | Тип            | Описание                                    |
| ------------- | -------------- | ------------------------------------------- |
| `id`          | UUID PK        | Уникальный идентификатор записи             |
| `device_id`   | UUID FK→Device | Устройство-источник                         |
| `module_id`   | UUID FK→Module | Конкретный модуль устройства                |
| `metric`      | VARCHAR(50)    | Название метрики (temperature, humidity)    |
| `value`       | DECIMAL(10,4)  | Числовое значение                           |
| `unit`        | VARCHAR(20)    | Единица измерения (°C, %, Pa)               |
| `recorded_at` | TIMESTAMPTZ    | Временная метка (ключ партиции TimescaleDB) |

## 3. Связи между сущностями

| Связь                    | Тип   | Описание                                               |
| ------------------------ | ----- | ------------------------------------------------------ |
| User → House             | 1 : M | Один пользователь владеет несколькими домами           |
| User ↔ House             | M : M | Доступ к дому через `UserHouseAccess` (owner/guest)    |
| House → Device           | 1 : M | Один дом содержит много устройств                      |
| DeviceType → Device      | 1 : M | Один тип описывает много устройств                     |
| Device → Module          | 1 : M | Одно устройство имеет один или несколько модулей       |
| Module → HeatingSchedule | 1 : M | Один модуль отопления может иметь несколько расписаний |
| Module → HeatingCommand  | 1 : M | Журнал всех команд, отправленных на модуль             |
| User → HeatingCommand    | 1 : M | Пользователь, выдавший команду                         |
| Device → TelemetryData   | 1 : M | Устройство генерирует поток телеметрии                 |
| Module → TelemetryData   | 1 : M | Конкретный модуль — источник метрики                   |
| Device → Alert           | 1 : M | Устройство может порождать множество алертов           |
| Module → Alert           | 1 : M | Алерт привязан к конкретному модулю                    |

## 4. ER-диаграмма (PlantUML)

Файл: [`docs/er-diagram.puml`](er-diagram.puml)

> Для рендеринга откройте `er-diagram.puml` в VS Code с расширением PlantUML  
> или вставьте содержимое на [plantuml.com](https://www.plantuml.com/plantuml/uml).

---

# Задание 4. Создание и документирование API

## 1. Выбор типов API

| Тип          | Когда используется                                                | Инструмент   |
| ------------ | ----------------------------------------------------------------- | ------------ |
| **REST API** | Синхронные запросы пользователя к микросервисам через API Gateway | OpenAPI 3.0  |
| **AsyncAPI** | Асинхронный обмен событиями между микросервисами через Kafka      | AsyncAPI 2.6 |

---

## 2. REST API — Device Management Service

**Файл:** [`docs/api/openapi.yaml`](api/openapi.yaml)

| Метод   | Путь                          | Описание                                                       |
| ------- | ----------------------------- | -------------------------------------------------------------- |
| `GET`   | `/api/v1/devices`             | Получить список устройств (фильтр по дому, статусу, пагинация) |
| `POST`  | `/api/v1/devices`             | Зарегистрировать новое устройство → публикует `device.created` |
| `GET`   | `/api/v1/devices/{id}`        | Получить устройство по UUID                                    |
| `PATCH` | `/api/v1/devices/{id}/status` | Обновить статус устройства → публикует `device.updated`        |

### Контракт: POST /api/v1/devices

**Запрос:**

```json
{
  "name": "Термостат спальни",
  "type_id": "7d2b3c1e-f1a2-4b56-8c9d-0e1f2a3b4c5d",
  "house_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "location": "bedroom",
  "serial_number": "TH-2024-002",
  "firmware_version": "2.1.0"
}
```

**Ответ 201:**

```json
{
  "id": "660f9511-f30c-52e5-b827-557766551111",
  "name": "Термостат спальни",
  "type": { "id": "...", "name": "Smart Thermostat", "category": "thermostat" },
  "house_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "location": "bedroom",
  "serial_number": "TH-2024-002",
  "status": "inactive",
  "firmware_version": "2.1.0",
  "installed_at": "2026-06-22T12:00:00Z"
}
```

| Код   | Ситуация                       |
| ----- | ------------------------------ |
| `201` | Устройство создано             |
| `400` | Невалидное тело запроса        |
| `401` | Отсутствует / невалидный JWT   |
| `409` | `serial_number` уже существует |
| `500` | Внутренняя ошибка сервера      |

---

## 3. REST API — Heating Control Service

| Метод  | Путь                                 | Описание                                             |
| ------ | ------------------------------------ | ---------------------------------------------------- |
| `POST` | `/api/v1/heating/{device_id}/on`     | Включить отопление → публикует `heating.turned_on`   |
| `POST` | `/api/v1/heating/{device_id}/off`    | Выключить отопление → публикует `heating.turned_off` |
| `GET`  | `/api/v1/heating/{device_id}/status` | Текущее состояние отопления                          |

### Контракт: POST /api/v1/heating/{device_id}/on

**Запрос (опционально):**

```json
{ "target_temperature": 22.5 }
```

**Ответ 200:**

```json
{
  "device_id": "550e8400-e29b-41d4-a716-446655440000",
  "state": "on",
  "target_temperature": 22.5,
  "command_id": "cmd-abc123",
  "updated_at": "2026-06-22T14:30:00Z"
}
```

| Код   | Ситуация                  |
| ----- | ------------------------- |
| `200` | Команда выполнена         |
| `404` | Устройство не найдено     |
| `409` | Отопление уже включено    |
| `500` | Ошибка выполнения команды |

---

## 4. REST API — Temperature Monitoring Service

| Метод | Путь                                      | Описание                              |
| ----- | ----------------------------------------- | ------------------------------------- |
| `GET` | `/api/v1/temperature/{location}`          | Текущая температура по местоположению |
| `GET` | `/api/v1/temperature/history/{device_id}` | История показаний (с агрегацией)      |
| `GET` | `/api/v1/temperature/alerts`              | Активные алерты по температуре        |

### Контракт: GET /api/v1/temperature/history/{device_id}

**Query-параметры:** `from`, `to` (ISO 8601), `interval` (`raw` / `1m` / `5m` / `1h` / `1d`)

**Ответ 200:**

```json
{
  "device_id": "550e8400-e29b-41d4-a716-446655440000",
  "location": "living_room",
  "interval": "5m",
  "data": [
    {
      "bucket": "2026-06-22T14:00:00Z",
      "avg": 22.4,
      "min": 21.9,
      "max": 22.8,
      "unit": "°C"
    },
    {
      "bucket": "2026-06-22T14:05:00Z",
      "avg": 22.5,
      "min": 22.1,
      "max": 22.9,
      "unit": "°C"
    }
  ]
}
```

---

## 5. AsyncAPI — Kafka Event Bus

**Файл:** [`docs/api/asyncapi.yaml`](api/asyncapi.yaml)

| Топик                 | Producer               | Consumers                               | Payload                   |
| --------------------- | ---------------------- | --------------------------------------- | ------------------------- |
| `device.created`      | Device Management      | Heating Control, Temperature Monitoring | DeviceCreatedPayload      |
| `device.updated`      | Device Management      | Heating Control, Temperature Monitoring | DeviceUpdatedPayload      |
| `heating.turned_on`   | Heating Control        | Device Management                       | HeatingCommandPayload     |
| `heating.turned_off`  | Heating Control        | Device Management                       | HeatingCommandPayload     |
| `temperature.reading` | Temperature Monitoring | (будущая аналитика)                     | TemperatureReadingPayload |
| `temperature.alert`   | Temperature Monitoring | (будущие нотификации)                   | TemperatureAlertPayload   |

### Контракт: temperature.alert

**Заголовки Kafka-сообщения:**

```json
{
  "event_id": "d4e5f6a7-b8c9-0123-4567-890123def012",
  "event_type": "temperature.alert",
  "source": "temperature-monitoring-service",
  "severity": "warning",
  "timestamp": "2026-06-22T03:15:00Z"
}
```

**Payload:**

```json
{
  "alert_id": "alert-uuid-001",
  "device_id": "550e8400-e29b-41d4-a716-446655440000",
  "house_id": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
  "location": "bedroom",
  "alert_type": "temperature_low",
  "value": 16.5,
  "threshold": 18.0,
  "unit": "°C",
  "message": "Temperature in bedroom dropped below 18°C",
  "triggered_at": "2026-06-22T03:15:00Z"
}
```

> **Рендеринг документации:**
>
> - OpenAPI: вставьте `openapi.yaml` на [editor.swagger.io](https://editor.swagger.io)
> - AsyncAPI: вставьте `asyncapi.yaml` на [studio.asyncapi.com](https://studio.asyncapi.com)
