// Seed demo Facilities into DynamoDB.
// Run locally after deploy (requires AWS credentials). Do not commit secrets.
//
//	go run . -table Facilities -region us-east-1
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type facilitySeed struct {
	FacilityID        string  `json:"facilityId"`
	Name              string  `json:"name"`
	District          string  `json:"district"`
	Lat               float64 `json:"lat"`
	Long              float64 `json:"long"`
	StatusOpen        bool    `json:"statusOpen"`
	StatusHasDoctor   bool    `json:"statusHasDoctor"`
	StatusHasMedicine bool    `json:"statusHasMedicine"`
	LastUpdatedBy     string  `json:"lastUpdatedBy"`
}

func main() {
	table := flag.String("table", "Facilities", "DynamoDB Facilities table name")
	region := flag.String("region", "us-east-1", "AWS region")
	dataFile := flag.String("file", "facilities.json", "Path to seed JSON")
	flag.Parse()

	raw, err := os.ReadFile(*dataFile)
	if err != nil {
		// Allow running from repo root.
		alt := filepath.Join("tools", "seed", "facilities.json")
		raw, err = os.ReadFile(alt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read seed file: %v\n", err)
			os.Exit(1)
		}
	}

	var facilities []facilitySeed
	if err := json.Unmarshal(raw, &facilities); err != nil {
		fmt.Fprintf(os.Stderr, "parse seed json: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(*region))
	if err != nil {
		fmt.Fprintf(os.Stderr, "aws config: %v\n", err)
		os.Exit(1)
	}
	client := dynamodb.NewFromConfig(cfg)
	now := time.Now().UTC().Format(time.RFC3339)

	for _, f := range facilities {
		_, err := client.PutItem(context.Background(), &dynamodb.PutItemInput{
			TableName: aws.String(*table),
			Item: map[string]types.AttributeValue{
				"facilityId":        &types.AttributeValueMemberS{Value: f.FacilityID},
				"name":              &types.AttributeValueMemberS{Value: f.Name},
				"district":          &types.AttributeValueMemberS{Value: f.District},
				"lat":               &types.AttributeValueMemberN{Value: strconv.FormatFloat(f.Lat, 'f', -1, 64)},
				"long":              &types.AttributeValueMemberN{Value: strconv.FormatFloat(f.Long, 'f', -1, 64)},
				"statusOpen":        &types.AttributeValueMemberBOOL{Value: f.StatusOpen},
				"statusHasDoctor":   &types.AttributeValueMemberBOOL{Value: f.StatusHasDoctor},
				"statusHasMedicine": &types.AttributeValueMemberBOOL{Value: f.StatusHasMedicine},
				"lastUpdatedBy":     &types.AttributeValueMemberS{Value: f.LastUpdatedBy},
				"lastUpdatedAt":     &types.AttributeValueMemberS{Value: now},
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "put %s: %v\n", f.FacilityID, err)
			os.Exit(1)
		}
		fmt.Printf("seeded %s (%s)\n", f.FacilityID, f.District)
	}
	fmt.Printf("done: %d facilities into %s\n", len(facilities), *table)
}
