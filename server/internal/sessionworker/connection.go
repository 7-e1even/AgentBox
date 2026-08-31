package sessionworker

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"agentbox/internal/workerprotocol"
	"github.com/coder/websocket"
)

func dialConnection(ctx context.Context, config workerConfig) (*websocket.Conn, error) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+config.credential)
	header.Set("User-Agent", "AgentBox-Session-Worker/3")
	header.Set(workerprotocol.HeaderMinimum, strconv.Itoa(workerprotocol.Minimum))
	header.Set(workerprotocol.HeaderMaximum, strconv.Itoa(workerprotocol.Current))
	conn, response, err := websocket.Dial(ctx, config.websocketURL(), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		if response != nil && response.StatusCode == http.StatusUpgradeRequired {
			return nil, fmt.Errorf("%w: install the Server's matching Worker release", workerprotocol.ErrIncompatible)
		}
		return nil, err
	}
	if _, err := workerprotocol.ValidateSelection(response.Header.Get(workerprotocol.HeaderSelected)); err != nil {
		conn.CloseNow()
		return nil, err
	}
	return conn, nil
}
