package auction

import (
	"context"
	"fmt"
	"fullcycle-auction_go/configuration/logger"
	"fullcycle-auction_go/internal/entity/auction_entity"
	"fullcycle-auction_go/internal/internal_error"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const (
	defaultAuctionDuration = 5 * time.Minute
	closeAuctionTimeout    = 10 * time.Second
)

type AuctionEntityMongo struct {
	Id          string                          `bson:"_id"`
	ProductName string                          `bson:"product_name"`
	Category    string                          `bson:"category"`
	Description string                          `bson:"description"`
	Condition   auction_entity.ProductCondition `bson:"condition"`
	Status      auction_entity.AuctionStatus    `bson:"status"`
	Timestamp   int64                           `bson:"timestamp"`
}
type AuctionRepository struct {
	Collection      *mongo.Collection
	auctionDuration time.Duration
}

func NewAuctionRepository(database *mongo.Database) *AuctionRepository {
	return &AuctionRepository{
		Collection:      database.Collection("auctions"),
		auctionDuration: GetAuctionDuration(),
	}
}

func (ar *AuctionRepository) CreateAuction(
	ctx context.Context,
	auctionEntity *auction_entity.Auction) *internal_error.InternalError {
	auctionEntityMongo := &AuctionEntityMongo{
		Id:          auctionEntity.Id,
		ProductName: auctionEntity.ProductName,
		Category:    auctionEntity.Category,
		Description: auctionEntity.Description,
		Condition:   auctionEntity.Condition,
		Status:      auctionEntity.Status,
		Timestamp:   auctionEntity.Timestamp.Unix(),
	}
	_, err := ar.Collection.InsertOne(ctx, auctionEntityMongo)
	if err != nil {
		logger.Error("Error trying to insert auction", err)
		return internal_error.NewInternalServerError("Error trying to insert auction")
	}

	go ar.closeAuctionAfter(auctionEntity.Id, ar.auctionDuration)

	return nil
}

// RescheduleOpenAuctions reagenda o fechamento dos leilões ainda Active, que
// perderiam o timer em memória após um restart da aplicação.
func (ar *AuctionRepository) RescheduleOpenAuctions(ctx context.Context) *internal_error.InternalError {
	cursor, err := ar.Collection.Find(ctx, bson.M{"status": auction_entity.Active})
	if err != nil {
		logger.Error("Error trying to find open auctions", err)
		return internal_error.NewInternalServerError("Error trying to find open auctions")
	}
	defer cursor.Close(ctx)

	var openAuctions []AuctionEntityMongo
	if err := cursor.All(ctx, &openAuctions); err != nil {
		logger.Error("Error trying to decode open auctions", err)
		return internal_error.NewInternalServerError("Error trying to decode open auctions")
	}

	for _, openAuction := range openAuctions {
		endTime := time.Unix(openAuction.Timestamp, 0).Add(ar.auctionDuration)
		remaining := time.Until(endTime)
		if remaining < 0 {
			remaining = 0
		}
		go ar.closeAuctionAfter(openAuction.Id, remaining)
	}

	return nil
}

// closeAuctionAfter espera a duração do leilão e então o marca como Completed.
// Roda em goroutine própria, com contexto independente do da requisição, que
// pode ser cancelado antes do leilão expirar.
func (ar *AuctionRepository) closeAuctionAfter(auctionId string, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	<-timer.C

	ctx, cancel := context.WithTimeout(context.Background(), closeAuctionTimeout)
	defer cancel()

	// O filtro por status Active mantém o update idempotente.
	filter := bson.M{"_id": auctionId, "status": auction_entity.Active}
	update := bson.M{"$set": bson.M{"status": auction_entity.Completed}}

	result, err := ar.Collection.UpdateOne(ctx, filter, update)
	if err != nil {
		logger.Error(fmt.Sprintf("Error trying to close auction id = %s", auctionId), err)
		return
	}
	if result.ModifiedCount > 0 {
		logger.Info(fmt.Sprintf("Auction id = %s closed automatically", auctionId))
	}
}

// GetAuctionDuration lê a duração dos leilões de AUCTION_DURATION, aceitando
// AUCTION_INTERVAL como fallback legado. Usa 5 minutos se nenhuma for válida.
func GetAuctionDuration() time.Duration {
	for _, name := range []string{"AUCTION_DURATION", "AUCTION_INTERVAL"} {
		if duration, err := time.ParseDuration(os.Getenv(name)); err == nil && duration > 0 {
			return duration
		}
	}

	return defaultAuctionDuration
}
