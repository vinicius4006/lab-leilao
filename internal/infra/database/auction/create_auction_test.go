package auction

import (
	"context"
	"fmt"
	"fullcycle-auction_go/internal/entity/auction_entity"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const defaultTestMongoURL = "mongodb://admin:admin@localhost:27017/?authSource=admin"

func newTestRepository(t *testing.T, duration time.Duration) *AuctionRepository {
	t.Helper()

	mongoURL := os.Getenv("TEST_MONGODB_URL")
	if mongoURL == "" {
		mongoURL = defaultTestMongoURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURL))
	if err != nil {
		t.Skipf("mongodb unavailable: %v", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("mongodb unavailable: %v", err)
	}

	database := client.Database(fmt.Sprintf("auctions_test_%s", uuid.NewString()))
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		database.Drop(cleanupCtx)
		client.Disconnect(cleanupCtx)
	})

	repository := NewAuctionRepository(database)
	repository.auctionDuration = duration
	return repository
}

func newTestAuction(t *testing.T) *auction_entity.Auction {
	t.Helper()

	auction, err := auction_entity.CreateAuction(
		"iPhone 12", "phones", "iPhone 12 usado em bom estado", auction_entity.Used)
	if err != nil {
		t.Fatalf("error creating auction entity: %v", err)
	}
	return auction
}

func TestCreateAuctionClosesAutomatically(t *testing.T) {
	const duration = time.Second
	repository := newTestRepository(t, duration)
	ctx := context.Background()
	auction := newTestAuction(t)

	if err := repository.CreateAuction(ctx, auction); err != nil {
		t.Fatalf("error creating auction: %v", err)
	}

	created, err := repository.FindAuctionById(ctx, auction.Id)
	if err != nil {
		t.Fatalf("error finding auction: %v", err)
	}
	if created.Status != auction_entity.Active {
		t.Fatalf("expected status Active right after creation, got %v", created.Status)
	}

	deadline := time.Now().Add(duration + 3*time.Second)
	for time.Now().Before(deadline) {
		found, err := repository.FindAuctionById(ctx, auction.Id)
		if err != nil {
			t.Fatalf("error finding auction: %v", err)
		}
		if found.Status == auction_entity.Completed {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("auction was not closed within %s", duration+3*time.Second)
}

func TestCreateAuctionDoesNotBlock(t *testing.T) {
	const duration = 2 * time.Second
	repository := newTestRepository(t, duration)

	start := time.Now()
	if err := repository.CreateAuction(context.Background(), newTestAuction(t)); err != nil {
		t.Fatalf("error creating auction: %v", err)
	}

	if elapsed := time.Since(start); elapsed >= duration {
		t.Fatalf("CreateAuction blocked for %s, expected to return before %s", elapsed, duration)
	}
}

func TestRescheduleOpenAuctionsClosesExpiredAuctions(t *testing.T) {
	repository := newTestRepository(t, time.Second)
	ctx := context.Background()

	// Leilão criado antes do restart: já passou da duração e continua Active.
	auction := newTestAuction(t)
	auction.Timestamp = time.Now().Add(-time.Minute)
	if _, err := repository.Collection.InsertOne(ctx, &AuctionEntityMongo{
		Id:          auction.Id,
		ProductName: auction.ProductName,
		Category:    auction.Category,
		Description: auction.Description,
		Condition:   auction.Condition,
		Status:      auction.Status,
		Timestamp:   auction.Timestamp.Unix(),
	}); err != nil {
		t.Fatalf("error inserting auction: %v", err)
	}

	if err := repository.RescheduleOpenAuctions(ctx); err != nil {
		t.Fatalf("error rescheduling auctions: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		found, err := repository.FindAuctionById(ctx, auction.Id)
		if err != nil {
			t.Fatalf("error finding auction: %v", err)
		}
		if found.Status == auction_entity.Completed {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("expired auction was not closed after rescheduling")
}

func TestGetAuctionDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration string
		interval string
		expected time.Duration
	}{
		{"uses AUCTION_DURATION", "30s", "", 30 * time.Second},
		{"AUCTION_DURATION wins over AUCTION_INTERVAL", "30s", "1m", 30 * time.Second},
		{"falls back to AUCTION_INTERVAL", "", "1m", time.Minute},
		{"invalid values use default", "abc", "", defaultAuctionDuration},
		{"empty values use default", "", "", defaultAuctionDuration},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AUCTION_DURATION", tt.duration)
			t.Setenv("AUCTION_INTERVAL", tt.interval)

			if got := GetAuctionDuration(); got != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, got)
			}
		})
	}
}
