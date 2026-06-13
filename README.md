# Ticket Online - Cinema Ticket Booking

Full-stack cinema seat booking demo for the take-home assignment. The system focuses on Google OAuth authentication, Redis distributed locking, realtime seat updates, and no double booking under concurrent requests.

## System Architecture Diagram

```text
Browser / Vue 3
  |  Google login, REST API, WebSocket
  v
Go Backend (:8080)
  |-- Google OAuth callback and JWT
  |-- Booking API and role middleware
  |-- WebSocket seat broadcasts
  |
  |-- MongoDB: users, showtimes, bookings, audit_logs
  |-- Redis: distributed seat locks
  `-- Redis Pub/Sub: booking events -> async audit logs/mock notification
```

## Tech Stack Overview

- Backend: Go, standard `net/http`, MongoDB driver, Redis client, Gorilla WebSocket
- Frontend: Vue 3 + Vite
- Database: MongoDB
- Distributed lock: Redis `SET key value NX EX 300`
- Message queue: Redis Pub/Sub
- Auth: Google OAuth 2.0, then app-issued JWT
- Deployment: Docker Compose

## Booking Flow

1. User clicks **Login with Google**.
2. Backend redirects to Google, receives the OAuth callback, upserts the user in MongoDB, assigns `USER` or `ADMIN`, and redirects back to Vue with a JWT.
3. Vue loads showtimes and opens a WebSocket for the selected showtime.
4. User chooses an available seat.
5. Backend creates a Redis distributed lock for 5 minutes and updates the MongoDB seat status to `LOCKED`.
6. Backend broadcasts the new seat map over WebSocket.
7. User confirms booking before the lock expires.
8. Backend conditionally changes the seat from `LOCKED` to `BOOKED`, creates a booking, publishes a booking event, and broadcasts again.
9. A background worker releases expired locks and publishes timeout/release audit events.

## Redis Lock Strategy

Each seat lock uses a key shaped like:

```text
seat-lock:{showtime_id}:{seat_id}
```

The backend creates the lock with `SET NX EX`, so only one user can acquire a specific seat lock during the 5-minute TTL. MongoDB updates are also conditional on the current seat status and lock owner. This two-layer strategy prevents double booking even if multiple users click the same seat at nearly the same time.

If MongoDB cannot mark the seat as `LOCKED`, the Redis key is deleted immediately. If payment confirmation is not completed before the TTL expires, a worker returns the seat to `AVAILABLE`.

## Message Queue Usage

Redis Pub/Sub is used as a real async message queue for booking domain events:

- `Booking Success`
- `Booking Timeout`
- `Seat Released`
- `Lock Fail`
- `System Error`

The consumer writes audit logs to MongoDB and prints a mock notification message. This keeps audit/notification work out of the synchronous booking path.

## How To Run

1. Copy the environment template:

```bash
cp .env.example .env
```

2. Create a Google OAuth client in Google Cloud Console.

3. Add this authorized redirect URI:

```text
http://localhost:8080/api/auth/google/callback
```

4. Fill these values in `.env`:

```text
GOOGLE_CLIENT_ID=your-client-id
GOOGLE_CLIENT_SECRET=your-client-secret
JWT_SECRET=replace-with-a-long-random-string
ADMIN_EMAILS=your-admin-email@gmail.com
```

5. Start the whole system:

```bash
docker compose up --build
```

6. Open:

```text
http://localhost:5173
```

## API Notes

- `GET /api/auth/google/login`
- `GET /api/auth/google/callback`
- `GET /api/auth/me`
- `GET /api/showtimes`
- `GET /api/showtimes/:id/seats`
- `POST /api/showtimes/:id/seats/:seatId/lock`
- `POST /api/bookings/confirm`
- `GET /api/admin/bookings`
- `GET /api/admin/audit-logs`
- `GET /ws?showtime_id=...&token=...`

Admin APIs require a Google account email listed in `ADMIN_EMAILS`.

## Assumptions & Trade-offs

- Google OAuth is real and required. There is no dev login fallback.
- Payment is mocked by the confirm booking endpoint.
- The UI is intentionally simple because the assignment emphasizes correctness, concurrency, and system design.
- Redis Pub/Sub is enough for this demo. A production system may prefer Kafka/RabbitMQ for durable event delivery.
- JWT is signed with HMAC using `JWT_SECRET`; use a strong secret outside local development.
