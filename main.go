package main

import (
	"context"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var templates *template.Template

type Message struct {
	ID      int       `json:"id"`
	Author  string    `json:"author"`
	Content string    `json:"content"`
	Created time.Time `json:"created"`
}

var (
	storeMu  sync.RWMutex
	messages = []Message{{ID: 1, Author: "System", Content: "Welcome to the SLRS-Admin devops web testing! -V1", Created: time.Now()}}
	nextID   = 2
)

type TemplateData struct {
	Title    string
	Flash    string
	Messages []Message
	Now      time.Time
}

func main() {
	if err := loadTemplates("templates"); err != nil {
		log.Fatalf("loading templates: %v", err)
	}
	mux := http.NewServeMux()
	staticDir := http.Dir("static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(staticDir)))
	mux.Handle("/", loggingMiddleware(http.HandlerFunc(indexHandler)))
	mux.Handle("/about", loggingMiddleware(http.HandlerFunc(aboutHandler)))
	mux.Handle("/submit", loggingMiddleware(http.HandlerFunc(submitHandler)))
	mux.Handle("/api/messages", loggingMiddleware(http.HandlerFunc(messagesAPIHandler)))

	srv := &http.Server{Addr: ":8080", Handler: mux, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	idle := make(chan struct{})
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Shutdown: %v", err)
		}
		close(idle)
	}()
	log.Printf("Server running on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("ListenAndServe: %v", err)
	}
	<-idle
	log.Println("Server stopped.")
}

func loadTemplates(dir string) error {
	t, err := template.ParseGlob(filepath.Join(dir, "*.html"))
	if err != nil {
		return err
	}
	templates = t
	return nil
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	storeMu.RLock()
	msgs := make([]Message, len(messages))
	copy(msgs, messages)
	storeMu.RUnlock()
	data := TemplateData{Title: "Home", Messages: msgs, Now: time.Now()}
	if err := templates.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		log.Println("template:", err)
	}
}

func aboutHandler(w http.ResponseWriter, r *http.Request) {
	data := TemplateData{Title: "About", Now: time.Now()}
	if err := templates.ExecuteTemplate(w, "about.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		log.Println("template:", err)
	}
}

func submitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	r.ParseForm()
	author := strings.TrimSpace(r.PostForm.Get("author"))
	content := strings.TrimSpace(r.PostForm.Get("content"))
	if content == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	storeMu.Lock()
	id := nextID
	nextID++
	msg := Message{ID: id, Author: author, Content: content, Created: time.Now()}
	messages = append([]Message{msg}, messages...)
	storeMu.Unlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func messagesAPIHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		storeMu.RLock()
		msgs := make([]Message, len(messages))
		copy(msgs, messages)
		storeMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(msgs)
	case http.MethodPost:
		var in struct{ Author, Content string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "Bad JSON", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(in.Content) == "" {
			http.Error(w, "content required", http.StatusBadRequest)
			return
		}
		storeMu.Lock()
		id := nextID
		nextID++
		msg := Message{ID: id, Author: in.Author, Content: in.Content, Created: time.Now()}
		messages = append([]Message{msg}, messages...)
		storeMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(msg)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(lrw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, lrw.status, time.Since(start))
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.status = code
	lrw.ResponseWriter.WriteHeader(code)
}
