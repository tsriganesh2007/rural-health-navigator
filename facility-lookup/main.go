package main

import (
	"context"
	"encoding/json"
	"log"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type facilityRecord struct {
	FacilityID        string   `json:"facilityId"`
	Name              string   `json:"name"`
	District          string   `json:"district"`
	Lat               *float64 `json:"lat"`
	Long              *float64 `json:"long"`
	StatusOpen        bool     `json:"statusOpen"`
	StatusHasDoctor   bool     `json:"statusHasDoctor"`
	StatusHasMedicine bool     `json:"statusHasMedicine"`
	LastUpdatedAt     string   `json:"lastUpdatedAt"`
}

type facilityLookupResponse struct {
	FacilityID        string   `json:"facilityId"`
	Name              string   `json:"name"`
	District          string   `json:"district"`
	Lat               *float64 `json:"lat"`
	Long              *float64 `json:"long"`
	StatusOpen        bool     `json:"statusOpen"`
	StatusHasDoctor   bool     `json:"statusHasDoctor"`
	StatusHasMedicine bool     `json:"statusHasMedicine"`
	LastUpdatedAt     string   `json:"lastUpdatedAt"`
	// SelectionMethod documents how the facility was chosen so GPS distance
	// ranking can be added later without changing the response shape.
	SelectionMethod string   `json:"selectionMethod"`
	DistanceKm      *float64 `json:"distanceKm"`
}

type errorBody struct {
	Error string `json:"error"`
}

// facilityLister reads facilities from DynamoDB (mocked in tests).
type facilityLister interface {
	ListFacilities(ctx context.Context) ([]facilityRecord, error)
}

var store facilityLister

func main() {
	tableName := strings.TrimSpace(os.Getenv("FACILITIES_TABLE_NAME"))
	if tableName == "" {
		log.Fatal("FACILITIES_TABLE_NAME must be set")
	}

	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}

	store = &dynamoFacilityLister{
		client:    dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}
	log.Printf("facility lookup lambda starting: table=%q", tableName)
	lambda.Start(handler)
}

type dynamoFacilityLister struct {
	client    *dynamodb.Client
	tableName string
}

func (d *dynamoFacilityLister) ListFacilities(ctx context.Context) ([]facilityRecord, error) {
	facilities := make([]facilityRecord, 0)
	var startKey map[string]types.AttributeValue

	for {
		out, err := d.client.Scan(ctx, &dynamodb.ScanInput{
			TableName:         aws.String(d.tableName),
			ExclusiveStartKey: startKey,
		})
		if err != nil {
			return nil, err
		}

		for _, item := range out.Items {
			facilities = append(facilities, facilityFromItem(item))
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		startKey = out.LastEvaluatedKey
	}

	return facilities, nil
}

func facilityFromItem(item map[string]types.AttributeValue) facilityRecord {
	return facilityRecord{
		FacilityID:        attrString(item, "facilityId"),
		Name:              attrString(item, "name"),
		District:          attrString(item, "district"),
		Lat:               attrFloatPtr(item, "lat"),
		Long:              attrFloatPtr(item, "long"),
		StatusOpen:        attrBool(item, "statusOpen"),
		StatusHasDoctor:   attrBool(item, "statusHasDoctor"),
		StatusHasMedicine: attrBool(item, "statusHasMedicine"),
		LastUpdatedAt:     attrString(item, "lastUpdatedAt"),
	}
}

func attrString(item map[string]types.AttributeValue, key string) string {
	if v, ok := item[key].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func attrBool(item map[string]types.AttributeValue, key string) bool {
	if v, ok := item[key].(*types.AttributeValueMemberBOOL); ok {
		return v.Value
	}
	return false
}

func attrFloatPtr(item map[string]types.AttributeValue, key string) *float64 {
	if v, ok := item[key].(*types.AttributeValueMemberN); ok {
		f, err := strconv.ParseFloat(v.Value, 64)
		if err != nil {
			return nil
		}
		return &f
	}
	return nil
}

func handler(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	district := strings.TrimSpace(request.QueryStringParameters["district"])
	if district == "" && request.Body != "" {
		var body struct {
			District string `json:"district"`
		}
		if err := json.Unmarshal([]byte(request.Body), &body); err == nil {
			district = strings.TrimSpace(body.District)
		}
	}
	if district == "" {
		log.Printf("validation failed: district missing")
		return jsonResponse(400, errorBody{Error: "district is required"})
	}

	log.Printf("facility lookup: district=%q", district)

	facilities, err := store.ListFacilities(ctx)
	if err != nil {
		log.Printf("facility scan failed: %v", err)
		return jsonResponse(500, errorBody{Error: "failed to look up facilities"})
	}

	selected, ok := selectAvailableFacility(facilities, district)
	if !ok {
		return jsonResponse(404, errorBody{Error: "no available facility found"})
	}

	return jsonResponse(200, selected)
}

// selectAvailableFacility picks an open, doctor-staffed facility in the district.
// Selection is deterministic by facilityId so results are stable without citizen GPS.
// Response keeps distanceKm null and selectionMethod set for a future GPS-based ranking.
func selectAvailableFacility(facilities []facilityRecord, district string) (facilityLookupResponse, bool) {
	want := strings.EqualFold
	candidates := make([]facilityRecord, 0)
	for _, f := range facilities {
		if !want(strings.TrimSpace(f.District), district) {
			continue
		}
		if !f.StatusOpen || !f.StatusHasDoctor {
			continue
		}
		candidates = append(candidates, f)
	}
	if len(candidates) == 0 {
		return facilityLookupResponse{}, false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].FacilityID < candidates[j].FacilityID
	})

	chosen := candidates[0]
	return facilityLookupResponse{
		FacilityID:        chosen.FacilityID,
		Name:              chosen.Name,
		District:          chosen.District,
		Lat:               chosen.Lat,
		Long:              chosen.Long,
		StatusOpen:        chosen.StatusOpen,
		StatusHasDoctor:   chosen.StatusHasDoctor,
		StatusHasMedicine: chosen.StatusHasMedicine,
		LastUpdatedAt:     chosen.LastUpdatedAt,
		SelectionMethod:   "district_deterministic",
		DistanceKm:        nil,
	}, true
}

// haversineKm is reserved for a future GPS-based nearest-facility ranking.
func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func jsonResponse(status int, body any) (events.APIGatewayProxyResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		log.Printf("failed to marshal response: %v", err)
		return events.APIGatewayProxyResponse{
			StatusCode: 500,
			Headers: map[string]string{
				"Content-Type":                "application/json",
				"Access-Control-Allow-Origin": "*",
			},
			Body: `{"error":"internal error"}`,
		}, nil
	}
	return events.APIGatewayProxyResponse{
		StatusCode: status,
		Headers: map[string]string{
			"Content-Type":                "application/json",
			"Access-Control-Allow-Origin": "*",
		},
		Body: string(b),
	}, nil
}
