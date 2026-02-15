package main

import (
	"context"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

var (
	chromeCheckOnce   sync.Once
	chromeAvailable   bool
	chromeCheckError  error
	chromeWarningOnce sync.Once
)

func checkChromeAvailable() (bool, error) {
	chromeCheckOnce.Do(func() {
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
		)
		allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
		defer cancel()

		ctx, cancel := chromedp.NewContext(allocCtx)
		defer cancel()

		ctx, cancel = context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return nil
		}))

		if err != nil {
			chromeCheckError = err
			chromeAvailable = false
			return
		}

		chromeAvailable = true
		chromeCheckError = nil
	})

	return chromeAvailable, chromeCheckError
}

var ErrChromeNotFound = newLocalizedError(msgErrChromeNotFound)
