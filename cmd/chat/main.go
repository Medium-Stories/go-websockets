package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gobackpack/websocket"
	"github.com/medium-stories/go-websockets/internal/web"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"net/http"
	"strings"
)

func main() {
	router := web.NewRouter()
	router.LoadHTMLFiles("cmd/chat/index.html")

	// serve frontend
	// this url will call /ws/:groupId (from frontend) which is going to establish ws connection
	router.GET("/join/:groupId", func(c *gin.Context) {
		c.HTML(200, "index.html", nil)
	})

	hub := websocket.NewHub()

	hubCtx, hubCancel := context.WithCancel(context.Background())
	hubFinished := hub.ListenForConnections(hubCtx)

	// connect client to group
	router.GET("/ws/:groupId", func(c *gin.Context) {
		groupId := c.Param("groupId")

		conn, err := websocket.NewGorillaConnectionAdapter(c.Writer, c.Request)
		if err != nil {
			logrus.Errorf("failed to upgrade connection: %s", err)
			return
		}
		// empty string for connId means the backend will generate it
		client, err := hub.EstablishConnection(conn, groupId, "")
		if err != nil {
			logrus.Errorf("failed to establish connection with groupId -> %s: %s", groupId, err)
			return
		}

		// send generated connection id back to frontend
		go hub.SendToConnectionId(groupId, client.ConnectionId, []byte(fmt.Sprintf("connection_id: %s", client.ConnectionId)))

		client.OnError = make(chan error)
		client.OnMessage = make(chan []byte)
		client.LostConnection = make(chan error)

		clientCtx, clientCancel := context.WithCancel(hubCtx)
		clientFinished := client.ReadMessages(clientCtx)

		// handle messages from frontend
		go func(clientCancel context.CancelFunc, client *websocket.Client) {
			defer clientCancel()

			for {
				select {
				case msg := <-client.OnMessage:
					logrus.Infof("client %s received message: %s", client.ConnectionId, msg)
					go hub.SendToGroup(groupId, msg)
				case err = <-client.OnError:
					logrus.Errorf("client %s received error: %s", client.ConnectionId, err)
				case err = <-client.LostConnection:
					go hub.DisconnectFromGroup(client.GroupId, client.ConnectionId)
					return
				}
			}
		}(clientCancel, client)

		logrus.Infof("client %s listening for messages...", client.ConnectionId)

		<-clientFinished

		logrus.Warnf("client %s stopped reading messages from ws", client.ConnectionId)
	})

	// disconnect client from group
	router.POST("/disconnect", func(c *gin.Context) {
		groupId := c.GetHeader("group_id")
		if strings.TrimSpace(groupId) == "" {
			c.JSON(http.StatusBadRequest, "missing group_id from headers")
			return
		}

		connId := c.GetHeader("connection_id")
		if strings.TrimSpace(connId) == "" {
			c.JSON(http.StatusBadRequest, "missing connection_id from headers")
			return
		}

		go hub.DisconnectFromGroup(groupId, connId)
	})

	// get all groups and clients
	router.GET("/connections", func(c *gin.Context) {
		c.JSON(http.StatusOK, hub.Groups)
	})

	web.ServeHttp(":8080", "chat room", router)
	hubCancel()

	<-hubFinished

	logrus.Warn("application stopped")
}
