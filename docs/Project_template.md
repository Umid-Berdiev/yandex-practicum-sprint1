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

> Для рендеринга откройте `c4-context.puml` в VS Code с расширением PlantUML  
> или вставьте содержимое на [plantuml.com/plantuml](https://www.plantuml.com/plantuml/uml).

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

> Для рендеринга откройте `.puml`-файлы в VS Code с расширением PlantUML  
> или вставьте содержимое на [plantuml.com](https://www.plantuml.com/plantuml/uml).
