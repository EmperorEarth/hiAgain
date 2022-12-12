package hiAgain

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"
)

func New(
	ctx context.Context,
	burstDuration time.Duration,
	maxRequestsPerBurst int,
	maxRequestsPerPeriod int,
	periodDuration time.Duration,
	remainingString string,
	resetString string,
) (*RateLimiter, error) {
	resetFloat, err := strconv.ParseFloat(resetString, 64)
	if err != nil {
		return nil, fmt.Errorf("strconv.ParseFloat (reset): %s", err)
	}
	resetPeriod := make(chan struct{})
	go func() {
		time.Sleep(time.Duration(resetFloat) * time.Second)
		resetPeriod <- struct{}{}
	}()
	remainingFloat, err := strconv.ParseFloat(remainingString, 64)
	if err != nil {
		return nil, fmt.Errorf("strconv.ParseFloat (remaining): %s", err)
	}
	rl := RateLimiter{
		ctx:                  ctx,
		clearPreviousBurst:   func() {},
		clearPreviousPeriod:  func() {},
		burstDuration:        burstDuration,
		burstReservations:    make(chan struct{}),
		burstTicker_:         time.NewTicker(burstDuration),
		maxRequestsPerBurst:  maxRequestsPerBurst,
		maxRequestsPerPeriod: maxRequestsPerPeriod,
		periodDuration:       periodDuration,
		periodReservations:   make(chan uint8),
		periodTicker_:        time.NewTicker(periodDuration),
		requestsQueue:        make(chan request),
		resetPeriod:          resetPeriod,
	}
	ctxWC, cancel := context.WithCancel(ctx)
	rl.clearPreviousPeriod = cancel
	go rl.managePeriod_()
	go rl.manageBurst_()
	go rl.listen_()
	go rl.refillPeriod_(ctxWC, 0, int(remainingFloat))
	return &rl, nil
}

func (rl RateLimiter) ResetPeriod() {
	rl.resetPeriod <- struct{}{}
}

func (rl_ *RateLimiter) Wait(ctx context.Context) error {
	select {
	case <-rl_.ctx.Done():
		return rl_.ctx.Err()
	default:
		select {
		case <-rl_.ctx.Done():
			return rl_.ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		default:
			wg := make(chan struct{})
			select {
			case <-rl_.ctx.Done():
				return rl_.ctx.Err()
			case <-ctx.Done():
				return ctx.Err()
			case rl_.requestsQueue <- request{
				ctx: ctx,
				wg:  wg,
			}:
				select {
				case <-rl_.ctx.Done():
					return rl_.ctx.Err()
				default:
					select {
					case <-rl_.ctx.Done():
						return rl_.ctx.Err()
					case <-ctx.Done():
						return ctx.Err()
					default:
						select {
						case <-rl_.ctx.Done():
							return rl_.ctx.Err()
						case <-ctx.Done():
							return ctx.Err()
						case <-wg:
							return nil
						}
					}
				}
			}
		}
	}
}

type RateLimiter struct {
	ctx           context.Context
	requestsQueue chan request

	burstMutex         sync.Mutex
	clearPreviousBurst func()

	burstDuration       time.Duration
	burstReservations   chan struct{}
	burstTicker_        *time.Ticker
	maxRequestsPerBurst int

	periodMutex         sync.Mutex
	clearPreviousPeriod func() // prevents issuing expired period reservations

	periodIDMutex sync.RWMutex
	periodID      uint8 // ensures when acquire burst token, period token is still valid

	maxRequestsPerPeriod int
	periodDuration       time.Duration
	periodReservations   chan uint8
	periodTicker_        *time.Ticker
	resetPeriod          chan struct{}
}

func (rl_ *RateLimiter) listen_() {
	for {
		select {
		case <-rl_.ctx.Done():
			return
		default:
			select {
			case <-rl_.ctx.Done():
				return
			case request := <-rl_.requestsQueue:
				go rl_.handle_(request)
			}
		}
	}
}

func (rl_ *RateLimiter) handle_(request request) {
	for {
		select {
		case <-rl_.ctx.Done():
			return
		default:
			select {
			case <-rl_.ctx.Done():
				return
			case <-request.ctx.Done():
				return
			default:
				select {
				case <-rl_.ctx.Done():
					return
				case <-request.ctx.Done():
					return
				case periodID := <-rl_.periodReservations:
					select {
					case <-rl_.ctx.Done():
						return
					default:
						select {
						case <-rl_.ctx.Done():
							return
						case <-request.ctx.Done():
							return
						default:
							select {
							case <-rl_.ctx.Done():
								return
							case <-request.ctx.Done():
								return
							case <-rl_.burstReservations:
								rl_.periodIDMutex.RLock()
								if periodID == rl_.periodID {
									rl_.periodIDMutex.RUnlock()
									request.wg <- struct{}{}
									return
								}
								rl_.periodIDMutex.RUnlock()
							}
						}
					}
				}
			}
		}
	}
}

func (rl_ *RateLimiter) managePeriod_() {
	for {
		select {
		case <-rl_.ctx.Done():
			return
		default:
			select {
			case <-rl_.ctx.Done():
				return
			case <-rl_.resetPeriod:
				ctx, cancel := context.WithCancel(rl_.ctx)
				rl_.periodMutex.Lock()
				rl_.periodTicker_.Reset(rl_.periodDuration)
				rl_.clearPreviousPeriod()
				id := rl_.advancePeriod()
				rl_.clearPreviousPeriod = cancel
				rl_.periodMutex.Unlock()
				go rl_.refillPeriod_(ctx, id, rl_.maxRequestsPerPeriod)
			default:
				select {
				case <-rl_.ctx.Done():
					return
				case <-rl_.resetPeriod:
					ctx, cancel := context.WithCancel(rl_.ctx)
					rl_.periodMutex.Lock()
					rl_.periodTicker_.Reset(rl_.periodDuration)
					rl_.clearPreviousPeriod()
					id := rl_.advancePeriod()
					rl_.clearPreviousPeriod = cancel
					rl_.periodMutex.Unlock()
					go rl_.refillPeriod_(ctx, id, rl_.maxRequestsPerPeriod)
				case <-rl_.periodTicker_.C:
					ctx, cancel := context.WithCancel(rl_.ctx)
					rl_.periodMutex.Lock()
					rl_.clearPreviousPeriod()
					id := rl_.advancePeriod()
					rl_.clearPreviousPeriod = cancel
					rl_.periodMutex.Unlock()
					go rl_.refillPeriod_(ctx, id, rl_.maxRequestsPerPeriod)
				}
			}
		}
	}
}

func (rl_ *RateLimiter) advancePeriod() uint8 {
	rl_.periodIDMutex.Lock()
	if rl_.periodID != math.MaxUint8 {
		rl_.periodID++
	} else {
		rl_.periodID = 0
	}
	id := rl_.periodID
	rl_.periodIDMutex.Unlock()
	return id
}

func (rl_ *RateLimiter) refillPeriod_(ctx context.Context, periodID uint8, remaining int) {
	for i := 0; i < remaining; i++ {
		select {
		case <-ctx.Done():
			return
		default:
			select {
			case <-ctx.Done():
				return
			case rl_.periodReservations <- periodID:
			}
		}
	}
}

func (rl_ *RateLimiter) manageBurst_() {
	for {
		select {
		case <-rl_.ctx.Done():
			return
		default:
			select {
			case <-rl_.ctx.Done():
				return
			case <-rl_.burstTicker_.C:
				ctx, cancel := context.WithCancel(rl_.ctx)
				rl_.burstMutex.Lock()
				rl_.clearPreviousBurst()
				rl_.clearPreviousBurst = cancel
				rl_.burstMutex.Unlock()
				go rl_.refillBurst_(ctx)
			}
		}
	}
}

func (rl_ *RateLimiter) refillBurst_(ctx context.Context) {
	for i := 0; i < rl_.maxRequestsPerBurst; i++ {
		select {
		case <-ctx.Done():
			return
		default:
			select {
			case <-ctx.Done():
				return
			case rl_.burstReservations <- struct{}{}:
			}
		}
	}
}

type request struct {
	ctx context.Context
	wg  chan struct{}
}
