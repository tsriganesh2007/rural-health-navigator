# Rural Health Access Navigator

Hackathon demo: non-diagnostic triage for rural communities, facility assignment, and a worker console.

**Stack:** Go, AWS SAM, Lambda, API Gateway, DynamoDB, SNS, plain HTML/CSS/vanilla JS.

## APIs

| Method | Path | Purpose |
|--------|------|---------|
| POST | `/triage` | Citizen triage (Gemini) + facility assignment |
| POST | `/triage/contact` | Optional doctor-contact follow-up request |
| GET | `/facilities/available` | Lookup open + doctor-staffed facility by district |
| POST | `/worker/login` | **Demo-only** worker login (not production auth) |
| POST | `/worker/status` | Update status for the logged-in worker's facility |
| GET | `/worker/queue?workerId=…` | Queue for that worker's district/facility |
| POST | `/worker/ack` | Acknowledge a pending session (conditional update) |

## Demo workers (NOT production authentication)

Password for all demo accounts: `demo123`

| Username | District | Facility |
|----------|----------|----------|
| warangal.worker | Warangal | WARANGAL-001 |
| hanamkonda.worker | Hanamkonda | HANAMKONDA-001 |
| karimnagar.worker | Karimnagar | KARIMNAGAR-001 |
| khammam.worker | Khammam | KHAMMAM-001 |
| nalgonda.worker | Nalgonda | NALGONDA-001 |
| nizamabad.worker | Nizamabad | NIZAMABAD-001 |
| adilabad.worker | Adilabad | ADILABAD-001 |
| mahabubnagar.worker | Mahabubnagar | MAHABUBNAGAR-001 |

Worker identity is resolved in the backend (`demodata.go`). The worker UI does **not** choose a facility. Login returns `workerId`, `district`, `facilityId`, and `facilityName`; queue and status updates use `workerId` only.

## Seeding Facilities

Workers live in code. **Facilities must be seeded into DynamoDB** after deploy so triage can assign a facility.

See [`scripts/SEED.md`](scripts/SEED.md) and:

```bash
cd tools/seed
go run . -table Facilities -region us-east-1
```

## Frontends

- `frontend/citizen/` — symptoms, district, optional contact number, triage result, facility info, doctor-contact request
- `frontend/worker/` — demo login, facility status for own facility, district-filtered queue, acknowledge, hide-acknowledged toggle

Set `API_BASE_URL` in each `app.js` to your API Gateway `…/Prod` base URL.

## Privacy notes (demo)

- Do not commit real API keys
- Contact numbers are stored on TriageSessions and shown only in the worker queue — not in SNS, not logged, not echoed in the citizen triage response
- Symptom text is not sent in SNS payloads
- Demo login is a fixed credential map — **not** Cognito / production auth

## Build & test (local)

```bash
# Unit tests (each module)
cd hello-world && go test ./...
cd ../facility-lookup && go test ./...
cd ../triage-contact && go test ./...
cd ../worker-login && go test ./...
cd ../worker-queue && go test ./...
cd ../worker-ack && go test ./...
cd ../worker-status && go test ./...

sam validate
sam build
```

Deploy yourself when ready (`sam deploy`). Supply `GeminiApiKey` via `--parameter-overrides`. Never commit a real key.
