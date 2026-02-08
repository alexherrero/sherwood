package strategies

import (
	"context"
	"fmt"
	
	"github.com/alexherrero/sherwood/backend/internal/data"
	"github.com/alexherrero/sherwood/backend/internal/engine"
)

type CrossoverStrategy struct {
	FastPeriod int
	SlowPeriod int
}

func NewCrossoverStrategy(fast, slow int) *CrossoverStrategy {
	return &CrossoverStrategy{
		FastPeriod: fast,
		SlowPeriod: slow,
	}
}

func (s *CrossoverStrategy) Name() string {
	return fmt.Sprintf("MA_Crossover_%d_%d", s.FastPeriod, s.SlowPeriod)
}

func (s *CrossoverStrategy) OnData(ctx context.Context, dataPayload interface{}) (*engine.Signal, error) {
	// TODO: meaningful implementation requiring state of past candles
	// For now, return nil
	
	candle, ok := dataPayload.(data.Candle)
	if !ok {
		return nil, nil
	}
	
	// Placeholder logic
	if candle.Close > 100 {
		return &engine.Signal{
			Symbol: "UNKNOWN",
			Action: "BUY",
			Price: candle.Close,
			Timestamp: candle.Timestamp,
			Strategy: s.Name(),
		}, nil
	}
	
	return nil, nil
}
