package relay

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/turn/v4"
)

const testSecret = "relay_test_secret_do_not_use"

// newTestRelay starts a relay on loopback. The relay binds ":0", so every test gets its own port and two
// tests running in parallel cannot answer each other's packets.
func newTestRelay(t *testing.T) *Service {
	t.Helper()
	service, err := New(Config{
		ListenUDP: "127.0.0.1:0",
		PublicIP:  "127.0.0.1",
		Realm:     "edumart.test",
		Secret:    testSecret,
		TTL:       2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

// allocate asks the relay for a relayed address, which is the operation a browser performs when it has no
// direct path. A returned error is the relay refusing, not a test failure — several tests assert on it.
func allocate(service *Service, username, password string) (net.PacketConn, error) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	client, err := turn.NewClient(&turn.ClientConfig{
		TURNServerAddr: service.Address(),
		Username:       username,
		Password:       password,
		Realm:          "edumart.test",
		Conn:           conn,
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := client.Listen(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client.Allocate()
}

// TestTheRelayRefusesAWrongPassword is the gate, shown failing.
//
// A TURN server that accepted anyone would be an open relay on the operator's bandwidth, which is the reason
// the credential exists at all. The same server, the same user, one byte of the password different.
func TestTheRelayRefusesAWrongPassword(t *testing.T) {
	service := newTestRelay(t)

	username, password, _ := service.Credentials("user-abc")
	if _, err := allocate(service, username, password); err != nil {
		t.Fatalf("the relay must accept a credential it minted: %v", err)
	}

	tampered := password
	if tampered[0] == 'A' {
		tampered = "B" + tampered[1:]
	} else {
		tampered = "A" + tampered[1:]
	}
	if _, err := allocate(service, username, tampered); err == nil {
		t.Fatal("the relay accepted a password that does not match the secret")
	}
}

// TestAnExpiredCredentialIsRefused proves the window is enforced by the server, not by the client forgetting.
func TestAnExpiredCredentialIsRefused(t *testing.T) {
	service := newTestRelay(t)

	stale := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10) + ":user-abc"
	if _, err := allocate(service, stale, Password(testSecret, stale)); err == nil {
		t.Fatal("the relay accepted a credential whose window has closed")
	}
}

// TestRelayForwardsAUDPPacket is the difference between "the port answers" and "media gets through".
//
// An allocation that succeeds but carries nothing would pass every check above, and would still leave a
// learner staring at a room where nobody can hear them.
func TestRelayForwardsAUDPPacket(t *testing.T) {
	service := newTestRelay(t)

	echo, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo socket: %v", err)
	}
	defer echo.Close()
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, from, readErr := echo.ReadFrom(buffer)
			if readErr != nil {
				return
			}
			_, _ = echo.WriteTo(rawCopy(buffer[:n]), from)
		}
	}()

	username, password, _ := service.Credentials("user-abc")
	relayed, err := allocate(service, username, password)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	defer relayed.Close()

	// The relay only forwards to addresses the client has permitted; a real client sends CreatePermission
	// before its first packet, and so does this one.
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("client socket: %v", err)
	}
	defer conn.Close()

	client, err := turn.NewClient(&turn.ClientConfig{
		TURNServerAddr: service.Address(),
		Username:       username,
		Password:       password,
		Realm:          "edumart.test",
		Conn:           conn,
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if err := client.Listen(); err != nil {
		t.Fatalf("listen: %v", err)
	}
	if _, err := client.Allocate(); err != nil {
		t.Fatalf("reallocate: %v", err)
	}
	if err := client.CreatePermission(echo.LocalAddr()); err != nil {
		t.Fatalf("permission: %v", err)
	}

	payload := []byte("edumart media probe")
	if _, err := relayed.WriteTo(payload, echo.LocalAddr()); err != nil {
		t.Fatalf("write through the relay: %v", err)
	}

	if err := relayed.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	buffer := make([]byte, 1024)
	n, _, err := relayed.ReadFrom(buffer)
	if err != nil {
		t.Fatalf("read back through the relay: %v", err)
	}
	if string(buffer[:n]) != string(payload) {
		t.Fatalf("the relay returned %q, expected %q", string(buffer[:n]), payload)
	}
}

// TestICEServersCarryPerUserCredentials covers what the browser is actually handed: one user's credential
// must not work as another's, and the URL must name the port the socket bound.
func TestICEServersCarryPerUserCredentials(t *testing.T) {
	service := newTestRelay(t)

	first := service.ICEServers("user-one")
	if len(first) != 1 {
		t.Fatalf("expected one iceServers entry, got %d", len(first))
	}
	entry := first[0]
	urls, ok := entry["urls"].([]string)
	if !ok || len(urls) != 1 || !strings.HasPrefix(urls[0], "turn:127.0.0.1:") {
		t.Fatalf("unexpected urls: %v", entry["urls"])
	}
	if !strings.HasSuffix(urls[0], "?transport=udp") {
		t.Fatalf("the URL must name its transport: %s", urls[0])
	}

	username := entry["username"].(string)
	if !strings.HasSuffix(username, ":user-one") {
		t.Fatalf("the username must carry the user, got %q", username)
	}
	if entry["credential"] != Password(testSecret, username) {
		t.Fatal("the credential does not match the REST scheme the server verifies with")
	}

	other := service.ICEServers("user-two")[0]
	if other["username"] == username || other["credential"] == entry["credential"] {
		t.Fatal("two users must not share a credential")
	}

	// The advertised port is the bound one, so a client can dial what it was told.
	if !strings.Contains(urls[0], portFrom(service.Address())) {
		t.Fatalf("the URL %q does not name the bound port %q", urls[0], service.Address())
	}
}

// TestNewRefusesConfigThatCannotWork keeps a misconfiguration at boot instead of at the first join.
func TestNewRefusesConfigThatCannotWork(t *testing.T) {
	if _, err := New(Config{ListenUDP: "127.0.0.1:0", PublicIP: "127.0.0.1"}); err == nil {
		t.Fatal("a relay without a secret must not start")
	}
	if _, err := New(Config{ListenUDP: "127.0.0.1:0", PublicIP: "not-an-ip", Secret: testSecret}); err == nil {
		t.Fatal("a relay without a usable public IP must not start")
	}
}

func portFrom(address string) string {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return port
}

func rawCopy(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}
