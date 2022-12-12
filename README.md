APIs may allow X requests over Y duration but it's inconsiderate to make X requests at the same time. `hiAgain` allows you to set and respect a second, self-imposed rate limit.

In `hiAgain`, the API's rate limit duration is called the `period` and your self-imposed rate limit is called the `burst`. If you're using an API that allows 500 calls per 5 minutes but also want to follow a self-imposed rate limit of 2 calls per second, then it would look like this with `hiAgain`:

```
// prepare to call API
limiter, err := hiAgain.New(
    serverContext,
    time.Second, // burst duration
    2, // max requests per burst
    500, // max requests per period
    5*time.Minute, // period duration
    "300", // contents of X-Rate-Limit-Remaining header
    "20", // contents of X-Rate-Limit-Reset header
)
if err != nil {} // handle error
if err := limiter.Wait(requestContext); err != nil {} // handle error
// call API here
```

## Notes:
* Untested and unoptimized
* Intended to be concurrent-safe
* When there's doubt, `hiAgain` errs on the side of leaving some meat on the bone rather than risking a 429 on either the period rate limit or your burst rate limit

## Name inspiration:
Play on [Christiaan *Huygens*](https://en.wikipedia.org/wiki/Christiaan_Huygens), [inventor of the centrifugal governor](https://en.wikipedia.org/wiki/Centrifugal_governor).
