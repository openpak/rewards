// Package core is the rewards service's client of the account core: who a web session
// belongs to, which accounts exist, each account's server-side play history, and the
// account event stream (friends, bans, deletions). Nothing here changes the core.
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
)

// ErrUnauthenticated is a session the core does not accept, or an account that is not active.
var ErrUnauthenticated = errors.New("core: not signed in")

type Client struct {
	Identity accountv1.IdentityClient
	Sessions accountv1.SessionsClient
	Links    accountv1.LinksClient
	Social   accountv1.SocialClient
	Events   accountv1.EventsClient
	Key      string
	HTTPURL  string
	HTTP     *http.Client
}

// Dial opens the gRPC connection. Insecure on purpose: the internal network is the boundary,
// as for every other internal caller of the core.
func Dial(grpcAddr, httpURL, key string) (*Client, error) {
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("core: dial: %w", err)
	}
	return &Client{
		Identity: accountv1.NewIdentityClient(conn),
		Sessions: accountv1.NewSessionsClient(conn),
		Links:    accountv1.NewLinksClient(conn),
		Social:   accountv1.NewSocialClient(conn),
		Events:   accountv1.NewEventsClient(conn),
		Key:      key, HTTPURL: httpURL,
		HTTP: &http.Client{Timeout: 5 * time.Second},
	}, nil
}

func (c *Client) ctx(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-internal-key", c.Key)
}

// Whoami resolves a web session through the core's GET /v1/whoami. Only an active account is
// signed in. The token is never logged.
func (c *Client) Whoami(ctx context.Context, bearer string) (string, error) {
	if bearer == "" {
		return "", ErrUnauthenticated
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.HTTPURL+"/v1/whoami", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("core: whoami: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", ErrUnauthenticated
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("core: whoami answered %d", resp.StatusCode)
	}
	var who struct {
		AccountID string `json:"account_id"`
		Status    string `json:"status"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&who); err != nil {
		return "", fmt.Errorf("core: whoami: %w", err)
	}
	if who.AccountID == "" || who.Status != "active" {
		return "", ErrUnauthenticated
	}
	return who.AccountID, nil
}

// Accounts is one page of the directory; next is empty at the end.
func (c *Client) Accounts(ctx context.Context, after string, limit int) (ids []string, next string, err error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 15*time.Second)
	defer cancel()
	resp, err := c.Identity.ListAccounts(rctx, &accountv1.ListAccountsRequest{AfterAccountId: after, Limit: int32(limit)})
	if err != nil {
		return nil, "", err
	}
	for _, a := range resp.GetAccounts() {
		ids = append(ids, a.GetAccountId())
	}
	return ids, resp.GetNextAfterAccountId(), nil
}

// Title is one row of an account's play history.
type Title struct {
	ID       string
	Seconds  int64
	Sessions int
}

// History is the account's per-title server-side session time in one namespace: every title (limit -1,
// account v0.23.0 and later; an older core still answers with its newest 20).
func (c *Client) History(ctx context.Context, account, namespace string) ([]Title, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 10*time.Second)
	defer cancel()
	resp, err := c.Sessions.GetPlayHistory(rctx, &accountv1.GetPlayHistoryRequest{AccountId: account, Namespace: namespace, Limit: -1})
	if err != nil {
		return nil, err
	}
	var out []Title
	for _, e := range resp.GetEntries() {
		out = append(out, Title{ID: e.GetTitleId(), Seconds: e.GetTotalSeconds(), Sessions: int(e.GetSessions())})
	}
	return out, nil
}

// PlayDays is the distinct UTC days (YYYY-MM-DD) the account played in sessions started before the cutoff.
func (c *Client) PlayDays(ctx context.Context, account string, before time.Time) ([]string, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 15*time.Second)
	defer cancel()
	resp, err := c.Sessions.GetPlayDays(rctx, &accountv1.GetPlayDaysRequest{AccountId: account, BeforeUnix: before.Unix()})
	if err != nil {
		return nil, err
	}
	return resp.GetDays(), nil
}

// Handler receives the events this service cares about.
type Handler interface {
	CoreCursor(ctx context.Context) (uint64, error)
	SetCoreCursor(ctx context.Context, v uint64) error
	Purge(ctx context.Context, account string) error
	SetFrozen(ctx context.Context, account string, frozen bool, actor string) error
	FriendAccepted(ctx context.Context, a, b string) error
}

// Follow consumes the core's event stream until ctx ends: deletions erase, bans freeze,
// unbans thaw, accepted friendships pay. A broken stream resumes from the saved cursor.
func (c *Client) Follow(ctx context.Context, h Handler) {
	for ctx.Err() == nil {
		err := c.followOnce(ctx, h)
		if ctx.Err() != nil {
			return
		}
		log.Printf("rewards: account core stream: %v; resuming in 5s", err)
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *Client) followOnce(ctx context.Context, h Handler) error {
	since, err := h.CoreCursor(ctx)
	if err != nil {
		return err
	}
	stream, err := c.Events.SubscribeAccountEvents(c.ctx(ctx), &accountv1.SubscribeAccountEventsRequest{SinceVersion: since})
	if err != nil {
		return err
	}
	saved := time.Now()
	for {
		ev, err := stream.Recv()
		if err != nil {
			return err
		}
		handled := true
		switch ev.GetType() {
		case "account_deleted":
			err = h.Purge(ctx, ev.GetAccountId())
		case "account_banned":
			err = h.SetFrozen(ctx, ev.GetAccountId(), true, "core")
		case "account_unbanned":
			err = h.SetFrozen(ctx, ev.GetAccountId(), false, "core")
		case "friend_accepted":
			err = h.FriendAccepted(ctx, ev.GetAccountId(), ev.GetSubjectId())
		default:
			handled = false
		}
		if err != nil {
			return fmt.Errorf("event %d (%s): %w", ev.GetVersion(), ev.GetType(), err)
		}
		if handled || time.Since(saved) > 5*time.Second {
			if err := h.SetCoreCursor(ctx, ev.GetVersion()); err != nil {
				return err
			}
			saved = time.Now()
		}
	}
}

// ErrNoLink is a network identity no active account owns.
var ErrNoLink = errors.New("core: no account for that identity")

type cached struct {
	val string
	exp time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]cached{}
)

func cacheGet(k string) (string, bool) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	c, ok := cache[k]
	if !ok || time.Now().After(c.exp) {
		return "", false
	}
	return c.val, true
}

func cachePut(k, v string, ttl time.Duration) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if len(cache) > 50000 {
		cache = map[string]cached{}
	}
	cache[k] = cached{v, time.Now().Add(ttl)}
}

// Resolve maps a network identity (adapter namespace and subject: Wii U/3DS PID, Switch BAAS
// user id, WFC user id, PSN or Xbox id) to the core account through the Links service. Active
// links only; answers are cached for ten minutes.
func (c *Client) Resolve(ctx context.Context, namespace, subject string) (string, error) {
	if namespace == "switch" {
		subject = strings.ToLower(subject)
	}
	k := namespace + "\x00" + subject
	if v, ok := cacheGet(k); ok {
		return v, nil
	}
	// A Wii U and a 3DS share one PID space, and the adapter registers the link under
	// whichever console created the account, so try the sibling namespace too.
	try := []string{namespace}
	switch namespace {
	case "wiiu":
		try = append(try, "3ds")
	case "3ds":
		try = append(try, "wiiu")
	}
	for _, ns := range try {
		rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
		resp, err := c.Links.GetLinkBySubject(rctx, &accountv1.GetLinkBySubjectRequest{Namespace: ns, SubjectId: subject})
		cancel()
		if status.Code(err) == codes.NotFound {
			continue
		}
		if err != nil {
			return "", err
		}
		if resp.GetState() != accountv1.LinkState_LINK_STATE_ACTIVE || resp.GetAccountId() == "" {
			continue
		}
		cachePut(k, resp.GetAccountId(), 10*time.Minute)
		return resp.GetAccountId(), nil
	}
	return "", ErrNoLink
}

// Friends is the set of accepted friends of an account.
func (c *Client) Friends(ctx context.Context, account string) (map[string]bool, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	resp, err := c.Social.ListFriends(rctx, &accountv1.ListFriendsRequest{AccountId: account})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, f := range resp.GetFriends() {
		out[strings.ToLower(f.GetAccountId())] = true
	}
	return out, nil
}

// ResolveHandle maps a public handle to its account id.
func (c *Client) ResolveHandle(ctx context.Context, handle string) (string, error) {
	rctx, cancel := context.WithTimeout(c.ctx(ctx), 5*time.Second)
	defer cancel()
	resp, err := c.Identity.ResolveHandle(rctx, &accountv1.ResolveHandleRequest{Handle: handle})
	if status.Code(err) == codes.NotFound {
		return "", ErrNoLink
	}
	if err != nil {
		return "", err
	}
	return resp.GetAccountId(), nil
}

// EntitlementChanged tells the universal stream that an account's game cosmetics changed (subject = game id).
func (c *Client) EntitlementChanged(ctx context.Context, acct, game string) error {
	_, err := c.Events.EmitEvent(c.ctx(ctx), &accountv1.EmitEventRequest{Type: "entitlement_changed", AccountId: acct, SubjectId: game})
	return err
}
