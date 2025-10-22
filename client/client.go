package main

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"image/color"
	"log"
	"net/rpc"
	"os"
	"strings"
	"sync"
	"time"
)

// ===== Tipos (DEVEM bater com o servidor) =====

type CreateUserRequest struct {
	Username string
}

type GetUserRequest struct{ Username string }

type SendMessageRequest struct {
	Sender   string
	Message  string
	Receiver string
}

type GetNewMessagesRequest struct{ Username string }

type Message struct {
	From     string
	To       string
	Body     string
	SentUnix int64
}

type User struct {
	Username    string
	ID          int
	PlayerColor color.RGBA
	Active      bool
}

// ===== Controle de prompt/prints =====

var outMu sync.Mutex

type promptKind int

const (
	pkNone promptKind = iota
	pkMessage
)

var curPrompt promptKind = pkNone

func printPromptReceiverLocked() { // requer outMu bloqueado
	// função mantida por compatibilidade; agora o servidor faz broadcast
	fmt.Print("Digite sua mensagem (será enviada a todos): ")
}

func printPromptMessageLocked() { // requer outMu bloqueado
	fmt.Print("Digite sua mensagem: ")
}

func redrawPromptLocked() { // requer outMu bloqueado
	switch curPrompt {
	case pkMessage:
		printPromptMessageLocked()
	default:
		// nada
	}
}

// Notificação vinda do polling: limpa a linha, mostra msg e redesenha o prompt atual
func notifyLine(s string) {
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Print("\r\033[2K") // limpa a linha atual
	fmt.Println(s)         // mostra a notificação
	redrawPromptLocked()   // redesenha o prompt correto (receiver ou message)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Uso: ./client <addr:port>")
		return
	}
	addr := os.Args[1]

	// Registrar tipos no gob
	gob.Register(CreateUserRequest{})
	gob.Register(GetUserRequest{})
	gob.Register(User{})
	gob.Register(SendMessageRequest{})
	gob.Register(GetNewMessagesRequest{})
	gob.Register(Message{})

	c, err := rpc.Dial("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	reader := bufio.NewReader(os.Stdin)

	// Login simples
	outMu.Lock()
	fmt.Print("Digite seu nome de usuário: ")
	outMu.Unlock()
	username, err := reader.ReadString('\n')
	if err != nil {
		log.Fatal(err)
	}
	username = strings.TrimSpace(username)

	// 1) CreateUser
	reqCreate := CreateUserRequest{Username: username}
	var me User
	if err := c.Call("UserService.CreateUser", &reqCreate, &me); err != nil {
		log.Fatal("RPC erro(CreateUser):", err)
	}
	fmt.Printf("Criado: Username=%s \n", me.Username)

	// 2) GetUser
	var got User
	if err := c.Call("UserService.GetUser", &GetUserRequest{Username: me.Username}, &got); err != nil {
		log.Fatal("RPC erro(GetUser):", err)
	}
	fmt.Printf("Buscado: Username=%s \n", got.Username)

	// 3) ListUsers
	var all []User
	if err := c.Call("UserService.ListUsers", &struct{}{}, &all); err != nil {
		log.Fatal("RPC erro(ListUsers):", err)
	}
	fmt.Println("Todos os usuários:")
	for _, it := range all {
		fmt.Printf("  - Username=%s \n", it.Username)
	}

	// 4) Polling de novas mensagens
	go func(myUser string) {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			var msgs []Message
			if err := c.Call("UserService.GetNewMessages", &GetNewMessagesRequest{Username: myUser}, &msgs); err != nil {
				log.Printf("RPC erro(GetNewMessages): %v", err)
				continue
			}
			for _, m := range msgs {
				notifyLine(fmt.Sprintf("[%s] %s", m.From, m.Body))
			}
		}
	}(me.Username)

	// 5) Loop de envio interativo (broadcast)
	for {
		outMu.Lock()
		curPrompt = pkMessage
		printPromptMessageLocked()
		outMu.Unlock()

		message, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("erro leitura message: %v", err)
			continue
		}
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}

		// Envia (receiver será ignorado pelo servidor)
		sendReq := SendMessageRequest{
			Sender:   me.Username,
			Message:  message,
			Receiver: "",
		}
		var ack struct{}
		if err := c.Call("UserService.SendMessage", &sendReq, &ack); err != nil {
			log.Printf("RPC erro(SendMessage): %v", err)
			continue
		}

		// Confirma envio
		outMu.Lock()
		fmt.Print("\r\033[2K") // limpa a linha do prompt de mensagem
		fmt.Println("Mensagem enviada (broadcast)!")
		curPrompt = pkMessage
		printPromptMessageLocked()
		outMu.Unlock()
	}
}
