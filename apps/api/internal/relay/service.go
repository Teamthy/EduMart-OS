// Package relay runs the TURN server that lets a participant behind a restrictive NAT reach the media
// forwarder.
//
// The forwarder cannot invent reachability. A browser on a network that blocks outbound UDP, or one behind a
// symmetric NAT, cannot be dialled from the server and cannot dial out on the port the server expected; without
// a relay that learner joins the room, sees the roster, and hears nothing. TURN is the standard answer and this
// package is a small, self-hosted one: it speaks the same protocol a hosted service would, so a deployment can
// replace it without touching a client.
//
// Credentials are ephemeral and per-user, minted on demand by the TURN REST scheme (draft-uberti-behave-turn-rest):
// the username is "<unix expiry>:<user id>" and the password is base64(HMAC-SHA1(secret, username)). Three
// consequences matter more than the mechanism:
//
//  1. **No static password in the client.** A page that shipped a permanent TURN password would leak one the
//     moment any browser cached it, and every user of the service would share the leak.
//  2. **The credential expires.** A captured username/password stops working when the window closes, so an
//     attacker who scrapes one gets a short ride rather than an open relay.
//  3. **The credential is attributable.** The user id is inside a value the server recomputes the password
//     from, so abuse can be traced to an account without the relay keeping its own session table.
//
// Holding no session table has a second consequence worth stating: a credential minted by one replica is
// accepted by any replica sharing the secret, so a fleet behind a UDP load balancer needs no sticky sessions.
package relay

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // HMAC-SHA1 is what the TURN REST credential scheme specifies.
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v4"
)

// DefaultTTL is how long a minted credential lives. A class is capped at 480 minutes, and a credential is
// re-minted every time the client asks for iceServers, so an hour covers a session and leaves little room to
// hoard a working credential.
const DefaultTTL = time.Hour

// Config describes one relay.
type Config struct {
	// ListenUDP is the address the server binds, e.g. "0.0.0.0:3478".
	ListenUDP string
	// PublicIP is the address clients are told to send to, and the address relays allocate on. It is required:
	// a relay that advertises the wrong address is worse than none, because the client waits for media that
	// nobody will ever send.
	PublicIP string
	// Realm is the TURN realm. It is part of the credential hash, so changing it invalidates live credentials.
	Realm string
	// Secret is the shared secret. It never reaches a client.
	Secret string
	// TTL is the credential lifetime. Zero means DefaultTTL.
	TTL time.Duration
	// Logger receives the relay's own lifecycle lines. Optional.
	Logger *slog.Logger
}

// Service is a running TURN server with a credential minter in front of it.
type Service struct {
	server *turn.Server
	conn   net.PacketConn
	cfg    Config
	ttl    time.Duration
	logger *slog.Logger
	// now is injectable so a test can mint an already-expired credential without sleeping through it.
	now func() time.Time
}

// New binds the listener and starts the server. The error is worth surfacing at boot rather than on first
// join: a relay that cannot bind is a deployment whose rooms will silently lose their far side.
func New(cfg Config) (*Service, error) {
	if strings.TrimSpace(cfg.Secret) == "" {
		return nil, errors.New("relay: a shared secret is required; a TURN server without one accepts anyone")
	}
	ip := net.ParseIP(strings.TrimSpace(cfg.PublicIP))
	if ip == nil {
		return nil, fmt.Errorf("relay: public IP %q is not an address; clients would send media nowhere", cfg.PublicIP)
	}
	if cfg.Realm == "" {
		cfg.Realm = "edumart"
	}
	listen := cfg.ListenUDP
	if listen == "" {
		listen = "0.0.0.0:3478"
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}

	conn, err := net.ListenPacket("udp4", listen)
	if err != nil {
		return nil, fmt.Errorf("relay: bind %s: %w", listen, err)
	}

	loggerFactory := logging.NewDefaultLoggerFactory()
	server, err := turn.NewServer(turn.ServerConfig{
		Realm: cfg.Realm,
		// The rest-API handler parses "<expiry>:<user>", refuses an expired timestamp, and recomputes the
		// password from the secret — the whole check, in the library, against the scheme we mint with.
		AuthHandler:   turn.LongTermTURNRESTAuthHandler(cfg.Secret, loggerFactory.NewLogger("relay")),
		LoggerFactory: loggerFactory,
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn: conn,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{
				// Relays allocate on the same address clients were told about; anything else is an address
				// nobody will send to.
				RelayAddress: ip,
				Address:      "0.0.0.0",
			},
		}},
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("relay: start turn server: %w", err)
	}

	service := &Service{
		server: server,
		conn:   conn,
		cfg:    cfg,
		ttl:    ttl,
		logger: cfg.Logger,
		now:    func() time.Time { return time.Now().UTC() },
	}
	if service.logger != nil {
		service.logger.Info("turn relay listening",
			"address", conn.LocalAddr().String(), "public_ip", ip.String(), "realm", cfg.Realm,
			"credential_ttl", ttl.String())
	}
	return service, nil
}

// Close stops the server and releases the socket.
func (s *Service) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	err := s.server.Close()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	return err
}

// Address is the bound address, which is what a test asserts on and what a log line should name.
func (s *Service) Address() string {
	if s == nil || s.conn == nil {
		return ""
	}
	return s.conn.LocalAddr().String()
}

// URLs are the TURN endpoints advertised to clients.
//
// The port is the one the socket actually bound, not the one in the configuration: with ":0" the
// configured port is a request, and advertising a port nothing listens on is the failure mode this whole
// package exists to prevent. A deployment that maps a different public port should bind that port.
func (s *Service) URLs() []string {
	if s == nil || s.conn == nil {
		return nil
	}
	_, port, err := net.SplitHostPort(s.conn.LocalAddr().String())
	if err != nil {
		port = "3478"
	}
	return []string{"turn:" + net.JoinHostPort(strings.TrimSpace(s.cfg.PublicIP), port) + "?transport=udp"}
}

// Credentials mints a username/password pair for one user, valid until the returned time.
//
// The user is embedded rather than looked up, so the relay holds no state about who is connected: an
// allocation either carries a credential this process could have minted, or it is refused.
func (s *Service) Credentials(userID string) (username, password string, expires time.Time) {
	expires = s.now().Add(s.ttl)
	username = strconv.FormatInt(expires.Unix(), 10) + ":" + userID
	return username, Password(s.cfg.Secret, username), expires
}

// ICEServers is the per-user iceServers list: the relay endpoints plus a credential for this user only.
func (s *Service) ICEServers(userID string) []map[string]any {
	if s == nil {
		return nil
	}
	username, password, _ := s.Credentials(userID)
	return []map[string]any{{
		"urls":       s.URLs(),
		"username":   username,
		"credential": password,
	}}
}

// Password is the TURN REST password for a username: base64(HMAC-SHA1(secret, username)). It is exported
// because a test that mints its own credential is more convincing than one that asks the server to grade its
// own homework.
func Password(secret, username string) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write([]byte(username)) //nolint:errcheck // hash.Hash never returns an error.
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
