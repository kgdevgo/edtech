# EdTech Backend in Go

A learning project showing the evolution of a small EdTech backend from a single in-memory binary to a production-style microservice setup with Kafka, Redis, PostgreSQL and Kubernetes manifests.

Each stage lives in its own directory and builds on the previous one.

---

## Stages

| Stage | Path | What it adds |
|-------|------|--------------|
| 1 | [`01_in_memory`](01_in_memory) | Single HTTP service, data in Go maps with a `sync.RWMutex` |
| 2 | [`02_with_pg`](02_with_pg) | PostgreSQL storage, JWT auth, migrations, middleware, rate limiting, Docker Compose |
| 3 | [`03_microservices`](03_microservices) | Split into `api`, `email-worker`, `payment-service`. Kafka + transactional outbox, Redis, Prometheus, k8s manifests, distributed tracing |

---

## Stage 1 — In-Memory

A minimal HTTP API to learn the standard library.

- `net/http` with Go 1.22+ method-pattern routing
- Two in-memory stores (`courses`, `students`) guarded by a mutex
- No persistence, no auth

**Endpoints**

```
POST /courses
GET  /courses
POST /students
POST /enroll
```

**Run**

```bash
cd 01_in_memory
go run .
# http://localhost:8080
```

---

## Stage 2 — PostgreSQL

The same domain, now with real persistence and the basics of a real web service.

- PostgreSQL via `database/sql` + `lib/pq`
- SQL migrations (`golang-migrate`)
- JWT auth (`POST /login`, `AuthMiddleware`)
- Middleware stack: `RequestID`, `Logging`, `Recovery`, `Timeout`, rate limiting
- Health endpoints: `GET /health`, `GET /ready`
- Docker Compose for app + Postgres + migrator
- Table-driven tests with `testcontainers-go`

**Run**

```bash
cd 02_with_pg
docker compose up --build
```

---

## Stage 3 — Microservices

Three services communicating through Kafka, plus supporting infrastructure.

### Services

- **`edtech-api`** — public HTTP API, writes domain events to an **outbox table**
- **`email-worker`** — Kafka consumer, sends emails on enrollment events, uses a `processed_events` table for idempotency
and routes unprocessable messages to a DLQ topic
- **`payment-service`** — handles payment events

### Infrastructure

- **PostgreSQL** — primary store, outbox pattern for exactly-once event publication
- **Kafka** — async messaging between services
- **Redis** — caching / rate limiting
- **Prometheus** — `/metrics` on each service
- **Structured logging** — `log/slog` JSON across all services
- **Distributed tracing** — `correlation_id` propagated through HTTP and Kafka headers
- **Kubernetes** — deployments, services, secrets and a migration `Job` in [`k8s/`](03_microservices/k8s)

### Layout

```
03_microservices/
├── cmd/
│   ├── api/         # HTTP API entrypoint
│   ├── worker/      # email-worker entrypoint
│   └── payment/     # payment-service entrypoint
├── internal/
│   ├── auth/        # JWT, password hashing
│   ├── config/      # env-based config
│   ├── events/      # Kafka topics & event models
│   ├── handlers/    # HTTP handlers + middleware + rate limit
│   ├── models/      # domain types
│   ├── storage/     # Postgres repository
│   └── worker/      # outbox relay + Kafka consumer
├── migrations/      # SQL migrations
├── k8s/             # Kubernetes manifests
└── docker-compose.yml
```

### API endpoints

| Method | Path        | Auth | Notes |
|--------|-------------|------|-------|
| POST   | `/login`    | —    | Returns JWT |
| POST   | `/courses`  | —    | |
| GET    | `/courses`  | —    | |
| POST   | `/students` | —    | |
| POST   | `/enroll`   | JWT  | Writes to outbox → Kafka |
| GET    | `/health`   | —    | Liveness |
| GET    | `/ready`    | —    | Readiness |
| GET    | `/metrics`  | —    | Prometheus |

### Run with Docker Compose

```bash
cd 03_microservices
cp .env.example .env   # fill in secrets
docker compose up --build
```

### Run on Kubernetes

```bash
cd 03_microservices/k8s
cp db-secret.example.yaml db-secret.yaml      # fill in
cp smtp-secret.example.yaml smtp-secret.yaml  # fill in

kubectl apply -f postgres.yaml
kubectl apply -f redis.yaml
kubectl apply -f kafka.yaml
kubectl apply -f migrations-configmap.yaml
kubectl apply -f migration-job.yaml
kubectl apply -f app-deployment.yaml
kubectl apply -f worker-deployment.yaml
kubectl apply -f payment-deployment.yaml
kubectl apply -f app-service.yaml
```

---

## Tech stack

**Language:** Go 1.26
**Storage:** PostgreSQL, Redis
**Messaging:** Kafka (`segmentio/kafka-go`)
**Auth:** JWT (`golang-jwt/jwt/v5`)
**Migrations:** `golang-migrate`
**Observability:** `log/slog`, Prometheus, correlation IDs
**Testing:** `testcontainers-go`
**Deploy:** Docker, Docker Compose, Kubernetes

---

## Patterns demonstrated

- Transactional **outbox** for reliable event publication
- **Idempotent consumer** via `processed_events` table
- Layered architecture (`cmd` / `internal/{handlers,storage,...}`)
- Repository interfaces + mock-based handler tests
- Graceful shutdown with `context.Context` and `signal.Notify`
- Middleware composition (`chain(...)`)
- Structured logging with request-scoped correlation IDs
- Dead Letter Queue (DLQ) for handling poison pills in Kafka

---

## Repository layout

```
.
├── 01_in_memory/     # Stage 1
├── 02_with_pg/       # Stage 2
└── 03_microservices/ # Stage 3
```