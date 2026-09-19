# Seeding demo Facilities (hackathon)

This project stores **demo workers in code** (`demodata.go` in worker Lambdas).
The **Facilities DynamoDB table** must be seeded once after deploy so triage assignment and facility lookup work.

## Option A — Go seed utility (recommended)

From the repo root, with AWS credentials configured for the target account/region:

```bash
cd tools/seed
go run . -table Facilities -region us-east-1
```

Optional flags:
- `-table` DynamoDB table name (default: `Facilities`)
- `-region` AWS region (default: `us-east-1`)

The utility upserts fictional demo facilities across Warangal, Hanamkonda, Karimnagar, Khammam, Nalgonda, Nizamabad, Adilabad, and Mahabubnagar. Each district has at least one open + doctor-staffed facility.

## Option B — AWS CLI (run yourself; do not commit credentials)

Example PutItem for one facility:

```bash
aws dynamodb put-item --table-name Facilities --item "{
  \"facilityId\": {\"S\": \"WARANGAL-001\"},
  \"name\": {\"S\": \"Warangal Demo Community Health Centre\"},
  \"district\": {\"S\": \"Warangal\"},
  \"lat\": {\"N\": \"17.9689\"},
  \"long\": {\"N\": \"79.5941\"},
  \"statusOpen\": {\"BOOL\": true},
  \"statusHasDoctor\": {\"BOOL\": true},
  \"statusHasMedicine\": {\"BOOL\": true},
  \"lastUpdatedBy\": {\"S\": \"seed\"},
  \"lastUpdatedAt\": {\"S\": \"2026-09-19T00:00:00Z\"}
}"
```

Repeat for each facility listed in `shared/demodata.go` / `tools/seed/facilities.json`.

## Demo worker login (in-code, not DynamoDB)

Workers are resolved by `POST /worker/login` from demo credentials such as:

| Username | Password | District | Facility |
|----------|----------|----------|----------|
| warangal.worker | demo123 | Warangal | WARANGAL-001 |
| hanamkonda.worker | demo123 | Hanamkonda | HANAMKONDA-001 |
| … | demo123 | … | … |

**Demo-only — not production authentication.**

## Notes

- Do not put real API keys or passwords in this repo.
- Facility names are fictional demo labels.
- After seeding, citizen triage can assign `facilityId` for the citizen's district.
