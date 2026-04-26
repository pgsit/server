package main

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Response struct {
	Message string `json:"message"`
	Time    string `json:"time"`
}

type RequestLog struct {
	ID         int    `json:"id"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	LatencyMs  int64  `json:"latency_ms"`
	RemoteAddr string `json:"remote_addr"`
	CreatedAt  string `json:"created_at"`
}

type Todo struct {
	ID          int    `json:"-"`
	Username    string `json:"username"`
	Description string `json:"description"`
	Date        string `json:"date"`
	Time        string `json:"time"`
	Past        bool   `json:"past"`
	CreatedAt   string `json:"-"`
}

var todoFormTmpl = template.Must(template.New("todo").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>Todo</title></head>
<body>
<h2>Add Todo</h2>
{{if .Saved}}<p style="color:green">Saved successfully!</p>{{end}}
{{if .Error}}<p style="color:red">{{.Error}}</p>{{end}}
<form method="POST" action="/todo" novalidate>
  <label>Name:<br><input type="text" name="username" value="{{.Username}}" required></label><br><br>
  <label>Date &amp; Time:<br><input type="datetime-local" name="datetime" value="{{.Datetime}}" required></label><br><br>
  <label>Description:<br><textarea name="description" rows="6" cols="50" required>{{.Description}}</textarea></label><br><br>
  <button type="submit">Save</button>
</form>
</body>
</html>`))

// statusRecorder wraps ResponseWriter to capture the written status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func initDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS request_logs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			method      TEXT    NOT NULL,
			path        TEXT    NOT NULL,
			status      INTEGER NOT NULL,
			latency_ms  INTEGER NOT NULL,
			remote_addr TEXT    NOT NULL,
			created_at  TEXT    NOT NULL
		)
	`)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS todos (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			username    TEXT    NOT NULL,
			description TEXT    NOT NULL,
			date        TEXT    NOT NULL,
			time        TEXT    NOT NULL,
			created_at  TEXT    NOT NULL
		)
	`)
	return db, err
}

func logRequest(db *sql.DB, method, path, remoteAddr string, status int, latency time.Duration) {
	_, err := db.Exec(
		`INSERT INTO request_logs (method, path, status, latency_ms, remote_addr, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		method, path, status, latency.Milliseconds(), remoteAddr,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		log.Printf("db log error: %v", err)
	}
}

func loggingMiddleware(db *sql.DB, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		latency := time.Since(start)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, latency)
		go logRequest(db, r.Method, r.URL.Path, r.RemoteAddr, rec.status, latency)
	})
}

func logsHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(
			`SELECT id, method, path, status, latency_ms, remote_addr, created_at FROM request_logs ORDER BY id DESC`,
		)
		if err != nil {
			http.Error(w, "failed to query logs", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var logs []RequestLog
		for rows.Next() {
			var l RequestLog
			if err := rows.Scan(&l.ID, &l.Method, &l.Path, &l.Status, &l.LatencyMs, &l.RemoteAddr, &l.CreatedAt); err != nil {
				http.Error(w, "failed to scan row", http.StatusInternalServerError)
				return
			}
			logs = append(logs, l)
		}
		if logs == nil {
			logs = []RequestLog{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(logs)
	}
}

var tasksViewTmpl = template.Must(template.New("tasksView").Parse(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>Tasks</title>
<style>
  body { font-family: sans-serif; padding: 1rem; }
  .nav { display: flex; justify-content: center; gap: 0.75rem; margin-bottom: 1.25rem; }
  .nav a {
    display: inline-block; padding: 0.4rem 1.2rem;
    border: 1px solid #888; border-radius: 4px;
    text-decoration: none; color: #333; background: #f5f5f5;
  }
  .nav a.active { background: #333; color: #fff; border-color: #333; }
  table { border-collapse: collapse; width: 100%; }
  th, td { border: 1px solid #ccc; padding: 0.5rem 1rem; text-align: left; }
  th { background: #f0f0f0; }
  .past td { color: green; }
  .del-btn {
    background: none; border: 1px solid #c00; color: #c00;
    padding: 0.2rem 0.6rem; border-radius: 3px; cursor: pointer; font-size: 0.85em;
  }
  .del-btn:hover { background: #c00; color: #fff; }
  .del-form { margin: 0; }
</style>
</head>
<body>
<h2 style="text-align:center">Tasks</h2>
<div class="nav">
  <a href="/tasks/view?filter=all"      {{if eq .Filter "all"}}class="active"{{end}}>All</a>
  <a href="/tasks/view?filter=today"    {{if eq .Filter "today"}}class="active"{{end}}>Today</a>
  <a href="/tasks/view?filter=tomorrow" {{if eq .Filter "tomorrow"}}class="active"{{end}}>Tomorrow</a>
  <a href="/tasks/view?filter=week"     {{if eq .Filter "week"}}class="active"{{end}}>Week</a>
</div>
{{if not .Tasks}}
<p style="text-align:center">No tasks found.</p>
{{else}}
<table>
  <thead><tr><th>Name</th><th>Description</th><th>Date</th><th>Time</th><th></th></tr></thead>
  <tbody>
  {{range .Tasks}}
  <tr class="{{if .Past}}past{{end}}">
    <td>{{.Username}}</td>
    <td>{{.Description}}</td>
    <td>{{.Date}}</td>
    <td>{{.Time}}</td>
    <td>
      {{if not .Past}}
      <form class="del-form" method="POST" action="/tasks/delete">
        <input type="hidden" name="id" value="{{.ID}}">
        <input type="hidden" name="filter" value="{{$.Filter}}">
        <button class="del-btn" type="submit">Delete</button>
      </form>
      {{end}}
    </td>
  </tr>
  {{end}}
  </tbody>
</table>
{{end}}
</body>
</html>`))

func deleteTaskHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		id := r.FormValue("id")
		filter := r.FormValue("filter")
		if filter == "" {
			filter = "all"
		}
		if id != "" {
			if _, err := db.Exec(`DELETE FROM todos WHERE id = ?`, id); err != nil {
				http.Error(w, "failed to delete task", http.StatusInternalServerError)
				return
			}
		}
		http.Redirect(w, r, "/tasks/view?filter="+filter, http.StatusSeeOther)
	}
}

func tasksViewHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := r.URL.Query().Get("filter")
		if filter == "" {
			filter = "all"
		}

		today := time.Now().Format("2006-01-02")
		tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
		week := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

		var query string
		var args []interface{}
		switch filter {
		case "today":
			query = `SELECT id, username, description, date, time, created_at FROM todos WHERE date = ? ORDER BY time`
			args = []interface{}{today}
		case "tomorrow":
			query = `SELECT id, username, description, date, time, created_at FROM todos WHERE date = ? ORDER BY time`
			args = []interface{}{tomorrow}
		case "week":
			query = `SELECT id, username, description, date, time, created_at FROM todos WHERE date >= ? AND date <= ? ORDER BY date, time`
			args = []interface{}{today, week}
		default:
			filter = "all"
			query = `SELECT id, username, description, date, time, created_at FROM todos ORDER BY date, time`
		}

		rows, err := db.Query(query, args...)
		if err != nil {
			http.Error(w, "failed to query todos", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var todos []Todo
		for rows.Next() {
			var t Todo
			if err := rows.Scan(&t.ID, &t.Username, &t.Description, &t.Date, &t.Time, &t.CreatedAt); err != nil {
				http.Error(w, "failed to scan row", http.StatusInternalServerError)
				return
			}
			t.Past = t.Date < today
			todos = append(todos, t)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		tasksViewTmpl.Execute(w, map[string]interface{}{"Tasks": todos, "Filter": filter})
	}
}

func tasksHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.Query(
			`SELECT id, username, description, date, time, created_at FROM todos ORDER BY date, time`,
		)
		if err != nil {
			http.Error(w, "failed to query todos", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var todos []Todo
		for rows.Next() {
			var t Todo
			if err := rows.Scan(&t.ID, &t.Username, &t.Description, &t.Date, &t.Time, &t.CreatedAt); err != nil {
				http.Error(w, "failed to scan row", http.StatusInternalServerError)
				return
			}
			t.Past = t.Date < time.Now().Format("2006-01-02")
			todos = append(todos, t)
		}
		if todos == nil {
			todos = []Todo{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(todos)
	}
}

func tasksByDateHandler(db *sql.DB, offset int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		date := time.Now().AddDate(0, 0, offset).Format("2006-01-02")

		rows, err := db.Query(
			`SELECT id, username, description, date, time, created_at FROM todos
			 WHERE date = ? ORDER BY time`,
			date,
		)
		if err != nil {
			http.Error(w, "failed to query todos", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var todos []Todo
		for rows.Next() {
			var t Todo
			if err := rows.Scan(&t.ID, &t.Username, &t.Description, &t.Date, &t.Time, &t.CreatedAt); err != nil {
				http.Error(w, "failed to scan row", http.StatusInternalServerError)
				return
			}
			t.Past = t.Date < time.Now().Format("2006-01-02")
			todos = append(todos, t)
		}
		if todos == nil {
			todos = []Todo{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(todos)
	}
}

func weeklyTasksHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		from := now.Format("2006-01-02")
		to := now.AddDate(0, 0, 7).Format("2006-01-02")

		rows, err := db.Query(
			`SELECT id, username, description, date, time, created_at FROM todos
			 WHERE date >= ? AND date <= ? ORDER BY date, time`,
			from, to,
		)
		if err != nil {
			http.Error(w, "failed to query todos", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var todos []Todo
		for rows.Next() {
			var t Todo
			if err := rows.Scan(&t.ID, &t.Username, &t.Description, &t.Date, &t.Time, &t.CreatedAt); err != nil {
				http.Error(w, "failed to scan row", http.StatusInternalServerError)
				return
			}
			t.Past = t.Date < time.Now().Format("2006-01-02")
			todos = append(todos, t)
		}
		if todos == nil {
			todos = []Todo{}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(todos)
	}
}

func todoHandler(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, saved := r.URL.Query()["saved"]
			todoFormTmpl.Execute(w, map[string]interface{}{
				"Saved":    saved,
				"Datetime": time.Now().Format("2006-01-02T15:04"),
			})
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			username := strings.TrimSpace(r.FormValue("username"))
			description := strings.TrimSpace(r.FormValue("description"))
			dt := r.FormValue("datetime") // "2006-01-02T15:04"
			if username == "" || description == "" || dt == "" {
				w.WriteHeader(http.StatusUnprocessableEntity)
				todoFormTmpl.Execute(w, map[string]interface{}{
					"Error":       "All fields are required.",
					"Username":    username,
					"Description": description,
					"Datetime":    dt,
				})
				return
			}
			parts := strings.SplitN(dt, "T", 2)
			date, timeStr := parts[0], ""
			if len(parts) == 2 {
				timeStr = parts[1]
			}
			_, err := db.Exec(
				`INSERT INTO todos (username, description, date, time, created_at) VALUES (?, ?, ?, ?, ?)`,
				username, description, date, timeStr, time.Now().UTC().Format(time.RFC3339),
			)
			if err != nil {
				http.Error(w, "failed to save todo", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/todo?saved", http.StatusSeeOther)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		Message: "ok",
		Time:    time.Now().UTC().Format(time.RFC3339),
	})
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(Response{
		Message: "Hello, World!",
		Time:    time.Now().UTC().Format(time.RFC3339),
	})
}

func main() {
	db, err := initDB("requests.db")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/hello", helloHandler)
	mux.HandleFunc("/logs", logsHandler(db))
	mux.HandleFunc("/todo", todoHandler(db))
	mux.Handle("/tasks", http.RedirectHandler("/tasks/view", http.StatusMovedPermanently))
	mux.HandleFunc("/tasks/view", tasksViewHandler(db))
	mux.HandleFunc("/tasks/delete", deleteTaskHandler(db))
	mux.HandleFunc("/tasks/weekly", weeklyTasksHandler(db))
	mux.HandleFunc("/tasks/today", tasksByDateHandler(db, 0))
	mux.HandleFunc("/tasks/tomorrow", tasksByDateHandler(db, 1))

	server := &http.Server{
		Addr:         ":8080",
		Handler:      loggingMiddleware(db, mux),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	log.Println("Server starting on :8080")
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
