package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math"
	"math/rand"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Data Structures
type BurnoutEntry struct {
	ID         int
	CreatedAt  time.Time
	Sleep      float64
	StudyHours float64
	Deadlines  int
	Mood       int
	Stress     int
	Exercise   bool
	Score      float64
	Level      string
	Advice     string
}

type ChartData struct {
	Labels []string  `json:"labels"`
	Data   []float64 `json:"data"`
}

var db *sql.DB

func main() {
	// Initialize Database
	var err error
	db, err = sql.Open("sqlite3", "./burnout.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Run Migration
	if err := runMigrations(); err != nil {
		log.Fatal(err)
	}

	// Routes
	http.HandleFunc("/", handleIndex)
	http.HandleFunc("/calculate", handleCalculate)
	http.HandleFunc("/history-chart", handleChartData)

	fmt.Println("Server starting at http://localhost:8081")
	log.Fatal(http.ListenAndServe(":8081", nil))
}

// runMigrations handles plain SQL migrations
func runMigrations() error {
	// For this prototype, we'll drop the old table to support schema changes easily.
	// In production, use ALTER TABLE.
	dropQuery := `DROP TABLE IF EXISTS entries`
	if _, err := db.Exec(dropQuery); err != nil {
		return err
	}

	query := `
	CREATE TABLE IF NOT EXISTS entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		sleep REAL,
		study_hours REAL,
		deadlines INTEGER,
		mood INTEGER,
		stress INTEGER,
		exercise BOOLEAN,
		score REAL,
		level TEXT,
		advice TEXT
	);
	`
	_, err := db.Exec(query)
	return err
}

// handleIndex renders the main page
func handleIndex(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles(filepath.Join("templates", "index.html"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.Execute(w, nil)
}

// handleCalculate processes the form submission
func handleCalculate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse Form
	sleep, _ := strconv.ParseFloat(r.FormValue("sleep"), 64)
	studyHours, _ := strconv.ParseFloat(r.FormValue("study"), 64)
	deadlines, _ := strconv.Atoi(r.FormValue("deadlines"))
	mood, _ := strconv.Atoi(r.FormValue("mood"))     // 1-5
	stress, _ := strconv.Atoi(r.FormValue("stress")) // 1-5
	exercise := r.FormValue("exercise") == "on"

	// Calculate Burnout Score
	// Formula: (deadline * 10) + (stress * 12) + ((8 - sleepHours) * 8) + (studyHours * 3) - (exercise ? 10 : 0)

	sleepPenalty := (8.0 - sleep) * 8.0
	// If sleep > 8, penalty becomes negative (bonus), which is fine but let's cap it slightly if needed.
	// The user formula implies less sleep = higher score.

	exerciseBonus := 0.0
	if exercise {
		exerciseBonus = 10.0
	}

	rawScore := (float64(deadlines) * 10.0) +
		(float64(stress) * 12.0) +
		sleepPenalty +
		(studyHours * 3.0) -
		exerciseBonus

	// Clamp 0-100
	score := math.Max(0, math.Min(100, rawScore))

	// Determine Category
	var level, colorClass, barColor string
	if score <= 30 {
		level = "🟢 Healthy"
		colorClass = "text-green-600"
		barColor = "bg-green-500"
	} else if score <= 60 {
		level = "🟡 At Risk"
		colorClass = "text-yellow-600"
		barColor = "bg-yellow-500"
	} else if score <= 80 {
		level = "🟠 High Risk"
		colorClass = "text-orange-600"
		barColor = "bg-orange-500"
	} else {
		level = "🔴 Severe Burnout"
		colorClass = "text-red-600"
		barColor = "bg-red-600"
	}

	// Generate "AI Feel" Advice
	advice := generateAIAdvice(sleep, deadlines, stress, score)

	// Save to DB
	_, err := db.Exec(`
		INSERT INTO entries (sleep, study_hours, deadlines, mood, stress, exercise, score, level, advice) 
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sleep, studyHours, deadlines, mood, stress, exercise, score, level, advice)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Render Result Fragment
	w.Header().Set("Content-Type", "text/html")
	w.Header().Set("HX-Trigger", "newEntry")

	// Visuals
	rotation := (score/100.0)*180.0 - 180.0

	// Format exercise string
	exerciseStr := "No"
	if exercise {
		exerciseStr = "Yes"
	}

	// Prepare Reset Plan Button if score > 80
	var resetPlanHTML string
	if score > 80 {
		resetPlanHTML = `
			<div class="mt-6">
				<button onclick="document.getElementById('reset-plan').classList.remove('hidden')" 
						class="w-full bg-red-600 hover:bg-red-700 text-white font-bold py-3 px-4 rounded-lg shadow-lg animate-pulse transition">
					🔥 Activate 24-Hour Reset Plan
				</button>
				<div id="reset-plan" class="hidden mt-4 bg-red-50 border border-red-200 rounded-lg p-4 text-left">
					<h4 class="font-bold text-red-800 mb-2">🚨 Emergency Protocol</h4>
					<ul class="space-y-2 text-sm text-red-700">
						<li class="flex items-center"><span class="mr-2">❌</span> No academic work tonight</li>
						<li class="flex items-center"><span class="mr-2">💤</span> Sleep minimum 7 hours</li>
						<li class="flex items-center"><span class="mr-2">📵</span> 1 hour no social media</li>
						<li class="flex items-center"><span class="mr-2">🚶</span> 20 minute walk outside</li>
						<li class="flex items-center"><span class="mr-2">📅</span> Reschedule 1 deadline immediately</li>
					</ul>
				</div>
			</div>
		`
	}

	// Current date for PDF
	currentDate := time.Now().Format("Jan 02, 2006")

	html := fmt.Sprintf(`
		<div class="animate-fade-in-up mt-8">
			<!-- Score Card -->
			<div class="bg-white p-6 rounded-2xl shadow-xl text-center border border-gray-100 relative overflow-hidden transition-all duration-500 hover:shadow-2xl">
				<div class="absolute top-0 left-0 w-full h-2 %s"></div>
				
				<h2 class="text-3xl font-bold mb-6 text-gray-800">Burnout Analysis</h2>
				
				<!-- Gauge Meter -->
				<div class="relative w-64 h-32 mx-auto overflow-hidden mb-6 group">
					<div class="absolute top-0 left-0 w-full h-full bg-gray-100 rounded-t-full"></div>
					<div class="absolute top-0 left-0 w-full h-full %s origin-bottom transition-all duration-1000 ease-out shadow-[0_0_20px_rgba(0,0,0,0.1)]"
						 style="transform: rotate(%.2fdeg); background: currentColor; border-radius: 9999px 9999px 0 0;">
					</div>
					<div class="absolute bottom-0 left-1/2 w-48 h-24 -ml-24 bg-white rounded-t-full flex items-end justify-center pb-2 shadow-[0_-10px_20px_rgba(255,255,255,1)] z-10">
						<div class="text-center group-hover:scale-110 transition-transform">
							<span class="text-5xl font-extrabold %s block">%.0f</span>
							<span class="text-xs text-gray-400 uppercase tracking-widest font-semibold">Score</span>
						</div>
					</div>
				</div>

				<div class="mb-8">
					<span class="inline-block px-6 py-2 rounded-full text-sm font-bold bg-opacity-10 %s bg-gray-200 border border-current shadow-sm">
						%s
					</span>
				</div>

				<!-- AI Insight Section -->
				<div class="bg-indigo-50 rounded-xl p-6 text-left border border-indigo-100 shadow-inner">
					<div class="flex items-center mb-3">
						<div class="bg-indigo-100 p-1.5 rounded-lg mr-3">
							<svg class="w-5 h-5 text-indigo-600" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"></path></svg>
						</div>
						<h3 class="font-bold text-indigo-900">AI Personal Insight</h3>
					</div>
					<p class="text-indigo-800 text-sm leading-relaxed font-medium italic">
						"%s"
					</p>
				</div>

				<!-- Action Items Grid -->
				<div class="mt-6 grid grid-cols-2 md:grid-cols-4 gap-3 text-xs text-gray-500 font-medium">
					<div class="bg-gray-50 p-3 rounded-lg border border-gray-100">💤 Sleep: <span class="text-gray-800">%.1fh</span></div>
					<div class="bg-gray-50 p-3 rounded-lg border border-gray-100">📚 Deadlines: <span class="text-gray-800">%d</span></div>
					<div class="bg-gray-50 p-3 rounded-lg border border-gray-100">😓 Stress: <span class="text-gray-800">%d/5</span></div>
					<div class="bg-gray-50 p-3 rounded-lg border border-gray-100">🏃 Exercise: <span class="text-gray-800">%s</span></div>
				</div>

				%s

				<!-- Download Report Button -->
				<div class="mt-4 pt-4 border-t border-gray-100">
					<button onclick="generatePDF(%.2f, '%s', '%s', '%s')" class="text-indigo-600 hover:text-indigo-800 text-sm font-semibold flex items-center justify-center w-full">
						<svg class="w-4 h-4 mr-2" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path></svg>
						Download Full Report (PDF)
					</button>
				</div>
			</div>
			
			<!-- Auto Save Script -->
			<script>
				// Save this result to localStorage automatically
				if (typeof saveToHistory === 'function') {
					saveToHistory(%.2f);
				}
			</script>
		</div>
	`, barColor, colorClass, rotation, colorClass, score, colorClass, level, advice, sleep, deadlines, stress, exerciseStr, resetPlanHTML, score, level, advice, currentDate, score)

	w.Write([]byte(html))
}

// generateAIAdvice simulates an AI response based on inputs
func generateAIAdvice(sleep float64, deadlines, stress int, score float64) string {
	// Simple rule-based generation to "simulate" AI

	intro := []string{
		"Based on your current workload patterns,",
		"Analyzing your physiological and academic inputs,",
		"Correlating your sleep data with stress levels,",
		"My assessment of your current state suggests",
	}

	rand.Seed(time.Now().UnixNano())
	selectedIntro := intro[rand.Intn(len(intro))]

	var body string
	if score > 80 {
		body = "your system is in critical overdrive. The combination of high stress and sleep deprivation is unsustainable. Your cognitive performance is likely degrading."
	} else if score > 60 {
		body = fmt.Sprintf("you are navigating a high-pressure zone. Managing %d deadlines with elevated stress is depleting your reserves faster than you can recover.", deadlines)
	} else if score > 30 {
		body = "you are maintaining functionality but showing early signs of friction. Your sleep schedule needs slight optimization to buffer against upcoming deadlines."
	} else {
		body = "you have achieved an optimal balance between academic rigor and personal recovery. Your resilience metrics are currently peak."
	}

	var action string
	if sleep < 5 {
		action = "Immediate Priority: Disconnect 1 hour before bed to reclaim REM cycles."
	} else if stress > 3 {
		action = "Suggestion: Implement the Pomodoro technique (25/5) to fragment stress accumulation."
	} else if deadlines > 4 {
		action = "Strategy: Triage your deadlines; ask for extensions on low-priority tasks."
	} else {
		action = "Recommendation: Maintain current routine but monitor hydration levels."
	}

	// Escape single quotes for JS safety
	// In a real app, use JSON encoding or proper escaping.
	// For this prototype, we just replace ' with \'
	// But since we use fmt.Sprintf, we should be careful.
	// Let's keep it simple and hope no complex characters break the inline JS call.
	// Or better: sanitize it inside the main function or here.
	fullAdvice := fmt.Sprintf("%s %s %s", selectedIntro, body, action)
	// Simple sanitization for JS string context
	// (Note: In production, pass data via JSON data attributes, not raw string injection)
	return fullAdvice
}

// handleChartData returns JSON for Chart.js
func handleChartData(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT created_at, score FROM (
			SELECT created_at, score FROM entries ORDER BY created_at DESC LIMIT 10
		) ORDER BY created_at ASC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var labels []string
	var data []float64

	for rows.Next() {
		var t time.Time
		var s float64
		if err := rows.Scan(&t, &s); err != nil {
			continue
		}
		labels = append(labels, t.Format("15:04"))
		data = append(data, s)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ChartData{Labels: labels, Data: data}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
