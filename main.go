package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
)

// ==================== CONFIG ====================

var (
	db                  *sql.DB
	discordClientID     = os.Getenv("DISCORD_CLIENT_ID")
	discordClientSecret = os.Getenv("DISCORD_CLIENT_SECRET")
	discordBotToken     = os.Getenv("DISCORD_BOT_TOKEN")
	sessionSecret       = os.Getenv("SESSION_SECRET")
	baseURL             = os.Getenv("BASE_URL")
)

// ==================== DATA STRUCTURES ====================

type Team struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Players []string `json:"players"` // Player 1-5
	Subs    []string `json:"subs"`    // Sub 1-4
}

type Week struct {
	ID       string   `json:"id"`
	Number   int      `json:"number"`
	Name     string   `json:"name"` // "Week 1", "SemiFinals", etc.
	Date     string   `json:"date"`
	Time     string   `json:"time"`
	Lobbies  []Lobby  `json:"lobbies"`
}

type Lobby struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"` // "Lobby 1", "Upper Bracket", etc.
	Teams    []string `json:"teams"` // Team names in this lobby
	Host     string   `json:"host,omitempty"`
	Streamer string   `json:"streamer,omitempty"`
}

type TeamAvailability struct {
	TeamID      string   `json:"teamId"`
	WeekID      string   `json:"weekId"`
	Available   []string `json:"available"`   // Player names available
	Unavailable []string `json:"unavailable"` // Player names unavailable
	SubsNeeded  int      `json:"subsNeeded"`  // How many subs needed
}

type User struct {
	DiscordID   string `json:"discordId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
	TeamID      string `json:"teamId"`   // Which team they're on
	PlayerName  string `json:"playerName"` // Their in-game name
	IsAdmin     bool   `json:"isAdmin"`
	IsManager   bool   `json:"isManager"` // Team captain
}

type Session struct {
	DiscordID   string
	Username    string
	DisplayName string
	Avatar      string
	TeamID      string
	PlayerName  string
	IsAdmin     bool
	IsManager   bool
	ExpiresAt   time.Time
}

// ==================== DATABASE ====================

func initDB() error {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL not set")
	}

	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		return err
	}

	// Create tables
	tables := []string{
		`CREATE TABLE IF NOT EXISTS teams (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			players TEXT DEFAULT '[]',
			subs TEXT DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS weeks (
			id TEXT PRIMARY KEY,
			number INTEGER NOT NULL,
			name TEXT NOT NULL,
			date TEXT,
			time TEXT,
			lobbies TEXT DEFAULT '[]'
		)`,
		`CREATE TABLE IF NOT EXISTS availability (
			id SERIAL PRIMARY KEY,
			team_id TEXT NOT NULL,
			week_id TEXT NOT NULL,
			available TEXT DEFAULT '[]',
			unavailable TEXT DEFAULT '[]',
			subs_needed INTEGER DEFAULT 0,
			UNIQUE(team_id, week_id)
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			discord_id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			display_name TEXT,
			avatar TEXT,
			team_id TEXT,
			player_name TEXT,
			is_admin BOOLEAN DEFAULT FALSE,
			is_manager BOOLEAN DEFAULT FALSE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT
		)`,
	}

	for _, table := range tables {
		if _, err := db.Exec(table); err != nil {
			return fmt.Errorf("failed to create table: %v", err)
		}
	}

	return nil
}

// ==================== SETTINGS ====================

func getSetting(key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = $1", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

func setSetting(key, value string) error {
	_, err := db.Exec(`
		INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = $2
	`, key, value)
	return err
}

// Team-specific settings (like webhook per team)
func getTeamSetting(teamName, key string) (string, error) {
	settingKey := fmt.Sprintf("team:%s:%s", teamName, key)
	return getSetting(settingKey)
}

func setTeamSetting(teamName, key, value string) error {
	settingKey := fmt.Sprintf("team:%s:%s", teamName, key)
	return setSetting(settingKey, value)
}

// ==================== TEAM FUNCTIONS ====================

func getAllTeams() ([]Team, error) {
	rows, err := db.Query("SELECT id, name, players, subs FROM teams ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var teams []Team
	for rows.Next() {
		var t Team
		var playersJSON, subsJSON string
		if err := rows.Scan(&t.ID, &t.Name, &playersJSON, &subsJSON); err != nil {
			continue
		}
		json.Unmarshal([]byte(playersJSON), &t.Players)
		json.Unmarshal([]byte(subsJSON), &t.Subs)
		teams = append(teams, t)
	}
	return teams, nil
}

func getTeamByID(id string) (*Team, error) {
	var t Team
	var playersJSON, subsJSON string
	err := db.QueryRow("SELECT id, name, players, subs FROM teams WHERE id = $1", id).
		Scan(&t.ID, &t.Name, &playersJSON, &subsJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(playersJSON), &t.Players)
	json.Unmarshal([]byte(subsJSON), &t.Subs)
	return &t, nil
}

func getTeamByName(name string) (*Team, error) {
	var t Team
	var playersJSON, subsJSON string
	err := db.QueryRow("SELECT id, name, players, subs FROM teams WHERE name = $1", name).
		Scan(&t.ID, &t.Name, &playersJSON, &subsJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(playersJSON), &t.Players)
	json.Unmarshal([]byte(subsJSON), &t.Subs)
	return &t, nil
}

func saveTeam(t Team) error {
	playersJSON, _ := json.Marshal(t.Players)
	subsJSON, _ := json.Marshal(t.Subs)
	_, err := db.Exec(`
		INSERT INTO teams (id, name, players, subs) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET name = $2, players = $3, subs = $4
	`, t.ID, t.Name, string(playersJSON), string(subsJSON))
	return err
}

// ==================== WEEK FUNCTIONS ====================

func getAllWeeks() ([]Week, error) {
	rows, err := db.Query("SELECT id, number, name, date, time, lobbies FROM weeks ORDER BY number")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var weeks []Week
	for rows.Next() {
		var w Week
		var lobbiesJSON string
		var date, timeVal sql.NullString
		if err := rows.Scan(&w.ID, &w.Number, &w.Name, &date, &timeVal, &lobbiesJSON); err != nil {
			continue
		}
		w.Date = date.String
		w.Time = timeVal.String
		json.Unmarshal([]byte(lobbiesJSON), &w.Lobbies)
		weeks = append(weeks, w)
	}
	return weeks, nil
}

func saveWeek(w Week) error {
	lobbiesJSON, _ := json.Marshal(w.Lobbies)
	_, err := db.Exec(`
		INSERT INTO weeks (id, number, name, date, time, lobbies) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET number = $2, name = $3, date = $4, time = $5, lobbies = $6
	`, w.ID, w.Number, w.Name, w.Date, w.Time, string(lobbiesJSON))
	return err
}

// ==================== AVAILABILITY FUNCTIONS ====================

func getTeamAvailability(teamID, weekID string) (*TeamAvailability, error) {
	var ta TeamAvailability
	var availJSON, unavailJSON string
	err := db.QueryRow(`
		SELECT team_id, week_id, available, unavailable, subs_needed
		FROM availability WHERE team_id = $1 AND week_id = $2
	`, teamID, weekID).Scan(&ta.TeamID, &ta.WeekID, &availJSON, &unavailJSON, &ta.SubsNeeded)

	if err == sql.ErrNoRows {
		return &TeamAvailability{TeamID: teamID, WeekID: weekID}, nil
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(availJSON), &ta.Available)
	json.Unmarshal([]byte(unavailJSON), &ta.Unavailable)
	return &ta, nil
}

func saveAvailability(ta TeamAvailability) error {
	availJSON, _ := json.Marshal(ta.Available)
	unavailJSON, _ := json.Marshal(ta.Unavailable)
	_, err := db.Exec(`
		INSERT INTO availability (team_id, week_id, available, unavailable, subs_needed)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (team_id, week_id) DO UPDATE SET
			available = $3, unavailable = $4, subs_needed = $5
	`, ta.TeamID, ta.WeekID, string(availJSON), string(unavailJSON), ta.SubsNeeded)
	return err
}

// ==================== USER FUNCTIONS ====================

func getUserByDiscordID(discordID string) (*User, error) {
	var u User
	err := db.QueryRow(`
		SELECT discord_id, username, display_name, avatar, team_id, player_name, is_admin, is_manager
		FROM users WHERE discord_id = $1
	`, discordID).Scan(&u.DiscordID, &u.Username, &u.DisplayName, &u.Avatar, &u.TeamID, &u.PlayerName, &u.IsAdmin, &u.IsManager)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func saveUser(u User) error {
	_, err := db.Exec(`
		INSERT INTO users (discord_id, username, display_name, avatar, team_id, player_name, is_admin, is_manager)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (discord_id) DO UPDATE SET
			username = $2, display_name = $3, avatar = $4, team_id = $5, player_name = $6, is_admin = $7, is_manager = $8
	`, u.DiscordID, u.Username, u.DisplayName, u.Avatar, u.TeamID, u.PlayerName, u.IsAdmin, u.IsManager)
	return err
}

// ==================== SESSION MANAGEMENT ====================

func createSessionToken(session Session) (string, error) {
	if sessionSecret == "" {
		sessionSecret = "genx-default-secret-change-me"
	}

	data := map[string]interface{}{
		"discord_id":   session.DiscordID,
		"username":     session.Username,
		"display_name": session.DisplayName,
		"avatar":       session.Avatar,
		"team_id":      session.TeamID,
		"player_name":  session.PlayerName,
		"is_admin":     session.IsAdmin,
		"is_manager":   session.IsManager,
		"expires_at":   session.ExpiresAt.Unix(),
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return "", err
	}

	h := hmac.New(sha256.New, []byte(sessionSecret))
	h.Write(jsonData)
	sig := base64.URLEncoding.EncodeToString(h.Sum(nil))

	token := base64.URLEncoding.EncodeToString(jsonData) + "." + sig
	return token, nil
}

func parseSessionToken(token string) *Session {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil
	}

	jsonData, err := base64.URLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil
	}

	h := hmac.New(sha256.New, []byte(sessionSecret))
	h.Write(jsonData)
	expectedSig := base64.URLEncoding.EncodeToString(h.Sum(nil))
	if parts[1] != expectedSig {
		return nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return nil
	}

	expiresAt := time.Unix(int64(data["expires_at"].(float64)), 0)
	if time.Now().After(expiresAt) {
		return nil
	}

	teamID := ""
	if v, ok := data["team_id"].(string); ok {
		teamID = v
	}
	playerName := ""
	if v, ok := data["player_name"].(string); ok {
		playerName = v
	}

	return &Session{
		DiscordID:   data["discord_id"].(string),
		Username:    data["username"].(string),
		DisplayName: data["display_name"].(string),
		Avatar:      data["avatar"].(string),
		TeamID:      teamID,
		PlayerName:  playerName,
		IsAdmin:     data["is_admin"].(bool),
		IsManager:   data["is_manager"].(bool),
		ExpiresAt:   expiresAt,
	}
}

func getSessionFromRequest(r *http.Request) *Session {
	cookie, err := r.Cookie("session")
	if err != nil {
		return nil
	}
	return parseSessionToken(cookie.Value)
}

// ==================== HTTP HELPERS ====================

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// ==================== AUTH HANDLERS ====================

func getDiscordOAuthURL() string {
	siteURL := baseURL
	if siteURL == "" {
		siteURL = "https://genx-league-calendar.onrender.com"
	}
	redirectURI := siteURL + "/auth/discord/callback"
	scope := "identify"
	return fmt.Sprintf(
		"https://discord.com/api/oauth2/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=%s",
		discordClientID,
		redirectURI,
		scope,
	)
}

func exchangeDiscordCode(code string) (string, error) {
	siteURL := baseURL
	if siteURL == "" {
		siteURL = "https://genx-league-calendar.onrender.com"
	}
	redirectURI := siteURL + "/auth/discord/callback"

	data := fmt.Sprintf(
		"client_id=%s&client_secret=%s&grant_type=authorization_code&code=%s&redirect_uri=%s",
		discordClientID, discordClientSecret, code, redirectURI,
	)

	req, _ := http.NewRequest("POST", "https://discord.com/api/oauth2/token", strings.NewReader(data))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if token, ok := result["access_token"].(string); ok {
		return token, nil
	}
	return "", fmt.Errorf("no access token in response")
}

func getDiscordUser(accessToken string) (map[string]interface{}, error) {
	req, _ := http.NewRequest("GET", "https://discord.com/api/users/@me", nil)
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var user map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&user)
	return user, nil
}

func handleDiscordLogin(w http.ResponseWriter, r *http.Request) {
	if discordClientID == "" {
		writeError(w, http.StatusServiceUnavailable, "Discord OAuth not configured")
		return
	}
	http.Redirect(w, r, getDiscordOAuthURL(), http.StatusTemporaryRedirect)
}

func handleDiscordCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Redirect(w, r, "/?error=no_code", http.StatusTemporaryRedirect)
		return
	}

	accessToken, err := exchangeDiscordCode(code)
	if err != nil {
		http.Redirect(w, r, "/?error=token_exchange", http.StatusTemporaryRedirect)
		return
	}

	discordUser, err := getDiscordUser(accessToken)
	if err != nil {
		http.Redirect(w, r, "/?error=user_fetch", http.StatusTemporaryRedirect)
		return
	}

	discordID := discordUser["id"].(string)
	username := discordUser["username"].(string)
	displayName := username
	if gn, ok := discordUser["global_name"].(string); ok && gn != "" {
		displayName = gn
	}

	avatarURL := ""
	if avatar, ok := discordUser["avatar"].(string); ok && avatar != "" {
		avatarURL = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", discordID, avatar)
	}

	// Check if user exists
	existingUser, _ := getUserByDiscordID(discordID)

	var teamID, playerName string
	var isAdmin, isManager bool

	if existingUser != nil {
		teamID = existingUser.TeamID
		playerName = existingUser.PlayerName
		isAdmin = existingUser.IsAdmin
		isManager = existingUser.IsManager
	}

	// Save/update user
	user := User{
		DiscordID:   discordID,
		Username:    username,
		DisplayName: displayName,
		Avatar:      avatarURL,
		TeamID:      teamID,
		PlayerName:  playerName,
		IsAdmin:     isAdmin,
		IsManager:   isManager,
	}
	saveUser(user)

	// Create session
	session := Session{
		DiscordID:   discordID,
		Username:    username,
		DisplayName: displayName,
		Avatar:      avatarURL,
		TeamID:      teamID,
		PlayerName:  playerName,
		IsAdmin:     isAdmin,
		IsManager:   isManager,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour),
	}

	token, _ := createSessionToken(session)

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60,
	})

	http.Redirect(w, r, "/", http.StatusTemporaryRedirect)
}

func handleAuthMe(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil {
		writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
		return
	}

	// Get team name from team ID
	teamName := ""
	if session.TeamID != "" {
		team, _ := getTeamByID(session.TeamID)
		if team != nil {
			teamName = team.Name
		} else {
			// TeamID might be the team name itself
			teamName = session.TeamID
		}
	}

	// Any linked team member can manage their team
	canManageTeam := teamName != "" && session.PlayerName != ""

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"authenticated": true,
		"discordId":     session.DiscordID,
		"username":      session.Username,
		"displayName":   session.DisplayName,
		"avatar":        session.Avatar,
		"teamName":      teamName,
		"playerName":    session.PlayerName,
		"isAdmin":       session.IsAdmin,
		"canManageTeam": canManageTeam,
	})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func handleLinkPlayer(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var body struct {
		TeamName   string `json:"teamName"`
		PlayerName string `json:"playerName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Update user in database
	user, _ := getUserByDiscordID(session.DiscordID)
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	// Store team name as the team ID (we use names as identifiers)
	user.TeamID = body.TeamName
	user.PlayerName = body.PlayerName
	saveUser(*user)

	// Update session
	session.TeamID = body.TeamName
	session.PlayerName = body.PlayerName
	token, _ := createSessionToken(*session)

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 24 * 60 * 60,
	})

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ==================== API HANDLERS ====================

func handleGetTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := getAllTeams()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, teams)
}

func handleGetWeeks(w http.ResponseWriter, r *http.Request) {
	weeks, err := getAllWeeks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, weeks)
}

func handleGetAvailability(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	teamID := vars["teamId"]
	weekID := vars["weekId"]

	avail, err := getTeamAvailability(teamID, weekID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, avail)
}

func handleSetAvailability(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Not authenticated")
		return
	}

	var body struct {
		WeekID    string `json:"weekId"`
		Available bool   `json:"available"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if session.TeamID == "" || session.PlayerName == "" {
		writeError(w, http.StatusBadRequest, "Please link your player profile first")
		return
	}

	avail, _ := getTeamAvailability(session.TeamID, body.WeekID)

	// Remove from both lists first
	avail.Available = removeFromSlice(avail.Available, session.PlayerName)
	avail.Unavailable = removeFromSlice(avail.Unavailable, session.PlayerName)

	// Add to appropriate list
	if body.Available {
		avail.Available = append(avail.Available, session.PlayerName)
	} else {
		avail.Unavailable = append(avail.Unavailable, session.PlayerName)
	}

	// Calculate subs needed (5 players needed, count how many unavailable from main roster)
	// Try to get team by ID first, then by name
	team, _ := getTeamByID(session.TeamID)
	if team == nil {
		team, _ = getTeamByName(session.TeamID)
	}
	if team != nil {
		unavailableMain := 0
		for _, player := range team.Players {
			for _, unavail := range avail.Unavailable {
				if player == unavail {
					unavailableMain++
					break
				}
			}
		}
		avail.SubsNeeded = unavailableMain
	}

	saveAvailability(*avail)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func handleGetAllAvailability(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || session.TeamID == "" || session.PlayerName == "" {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	weeks, _ := getAllWeeks()
	var result []map[string]interface{}

	for _, week := range weeks {
		avail, _ := getTeamAvailability(session.TeamID, week.ID)
		// Check if this player is in available or unavailable list
		isAvailable := false
		hasResponded := false

		for _, p := range avail.Available {
			if p == session.PlayerName {
				isAvailable = true
				hasResponded = true
				break
			}
		}
		for _, p := range avail.Unavailable {
			if p == session.PlayerName {
				isAvailable = false
				hasResponded = true
				break
			}
		}

		if hasResponded {
			result = append(result, map[string]interface{}{
				"weekId":    week.ID,
				"available": isAvailable,
			})
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// Get full team availability for a week (for team members to see who's confirmed)
func handleGetTeamWeekAvailability(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || session.TeamID == "" {
		writeError(w, http.StatusUnauthorized, "Must be logged in and linked to a team")
		return
	}

	vars := mux.Vars(r)
	weekID := vars["weekId"]

	avail, err := getTeamAvailability(session.TeamID, weekID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Get team roster for context
	team, _ := getTeamByName(session.TeamID)
	if team == nil {
		team, _ = getTeamByID(session.TeamID)
	}

	roster := []string{}
	subs := []string{}
	if team != nil {
		roster = team.Players
		subs = team.Subs
	}

	// Figure out who hasn't responded
	responded := make(map[string]bool)
	for _, p := range avail.Available {
		responded[p] = true
	}
	for _, p := range avail.Unavailable {
		responded[p] = true
	}

	notResponded := []string{}
	for _, p := range roster {
		if !responded[p] {
			notResponded = append(notResponded, p)
		}
	}
	for _, s := range subs {
		if !responded[s] {
			notResponded = append(notResponded, s)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"weekId":       weekID,
		"teamName":     session.TeamID,
		"available":    avail.Available,
		"unavailable":  avail.Unavailable,
		"notResponded": notResponded,
		"subsNeeded":   avail.SubsNeeded,
		"roster":       roster,
		"subs":         subs,
	})
}

// ==================== DISCORD WEBHOOK ====================

func handleGetWebhook(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || session.TeamID == "" {
		writeError(w, http.StatusUnauthorized, "Must be logged in and linked to a team")
		return
	}

	// Get team-specific webhook
	webhook, _ := getTeamSetting(session.TeamID, "webhook")
	response := map[string]interface{}{
		"configured": webhook != "",
		"webhookUrl": webhook,
	}

	writeJSON(w, http.StatusOK, response)
}

func handleSetWebhook(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || session.TeamID == "" {
		writeError(w, http.StatusUnauthorized, "Must be logged in and linked to a team")
		return
	}

	var body struct {
		WebhookUrl string `json:"webhookUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Save team-specific webhook
	if err := setTeamSetting(session.TeamID, "webhook", body.WebhookUrl); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func handleAnnounceWeek(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || session.TeamID == "" {
		writeError(w, http.StatusUnauthorized, "Must be logged in and linked to a team")
		return
	}

	var body struct {
		WeekID string `json:"weekId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	weekID := body.WeekID

	// Get team-specific webhook
	webhook, _ := getTeamSetting(session.TeamID, "webhook")
	if webhook == "" {
		writeError(w, http.StatusBadRequest, "Team webhook not configured. Set it in Team Settings.")
		return
	}

	weeks, _ := getAllWeeks()
	var week *Week
	for _, wk := range weeks {
		if wk.ID == weekID {
			week = &wk
			break
		}
	}
	if week == nil {
		writeError(w, http.StatusNotFound, "Week not found")
		return
	}

	// Find which lobby this team is in
	teamName := session.TeamID
	var userLobby *Lobby
	for _, lobby := range week.Lobbies {
		for _, t := range lobby.Teams {
			if t == teamName {
				userLobby = &lobby
				break
			}
		}
		if userLobby != nil {
			break
		}
	}

	siteURL := baseURL
	if siteURL == "" {
		siteURL = "https://genx-league-calendar.onrender.com"
	}
	weekLink := siteURL + "/?week=" + weekID

	// Get current availability for the team
	avail, _ := getTeamAvailability(teamName, weekID)

	// Build team-specific message
	dateStr := week.Date
	if dateStr == "" {
		dateStr = "TBD"
	}
	timeStr := week.Time
	if timeStr == "" {
		timeStr = "8:00 PM ET"
	}

	var fields []map[string]interface{}

	// Add lobby info if found
	if userLobby != nil {
		opponents := []string{}
		for _, t := range userLobby.Teams {
			if t != teamName {
				opponents = append(opponents, t)
			}
		}
		fields = append(fields, map[string]interface{}{
			"name":   "🎮 " + userLobby.Name,
			"value":  "Playing against:\n" + strings.Join(opponents, "\n"),
			"inline": true,
		})
	}

	// Add current availability status
	availCount := len(avail.Available)
	unavailCount := len(avail.Unavailable)
	statusText := fmt.Sprintf("✅ Available: %d\n❌ Unavailable: %d", availCount, unavailCount)
	if avail.SubsNeeded > 0 {
		statusText += fmt.Sprintf("\n⚠️ **Need %d sub(s)!**", avail.SubsNeeded)
	}
	fields = append(fields, map[string]interface{}{
		"name":   "📊 Current Status",
		"value":  statusText,
		"inline": true,
	})

	// Add link
	fields = append(fields, map[string]interface{}{
		"name":   "✅ Confirm Your Availability",
		"value":  fmt.Sprintf("[Click here to respond](%s)", weekLink),
		"inline": false,
	})

	embed := map[string]interface{}{
		"title":       fmt.Sprintf("📢 %s - %s @ %s", week.Name, dateStr, timeStr),
		"description": fmt.Sprintf("**%s** - Please confirm if you can play!", teamName),
		"color":       0xf59e0b,
		"fields":      fields,
		"footer":      map[string]string{"text": "GenX League • Reply ASAP!"},
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	}

	payload := map[string]interface{}{
		"username": "GenX League",
		"content":  "@everyone " + week.Name + " is coming up! Please confirm your availability.",
		"embeds":   []map[string]interface{}{embed},
	}

	payloadBytes, _ := json.Marshal(payload)
	resp, err := http.Post(webhook, "application/json", bytes.NewReader(payloadBytes))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to post to Discord: "+err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		writeError(w, http.StatusInternalServerError, "Discord API error: "+string(respBody))
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ==================== ADMIN HANDLERS ====================

func handleImportTeams(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var teams []Team
	if err := json.NewDecoder(r.Body).Decode(&teams); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	for _, t := range teams {
		if t.ID == "" {
			t.ID = strings.ToLower(strings.ReplaceAll(t.Name, " ", "-"))
		}
		saveTeam(t)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"count":   len(teams),
	})
}

func handleImportWeeks(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var weeks []Week
	if err := json.NewDecoder(r.Body).Decode(&weeks); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	for _, w := range weeks {
		saveWeek(w)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"count":   len(weeks),
	})
}

func handleSetAdmin(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var body struct {
		DiscordID string `json:"discordId"`
		IsAdmin   bool   `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	user, _ := getUserByDiscordID(body.DiscordID)
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found")
		return
	}

	user.IsAdmin = body.IsAdmin
	saveUser(*user)

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ==================== HELPERS ====================

func removeFromSlice(slice []string, item string) []string {
	result := []string{}
	for _, s := range slice {
		if s != item {
			result = append(result, s)
		}
	}
	return result
}

// ==================== STATIC FILES ====================

func serveStatic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.html"
	}

	// Determine content type
	contentType := "text/plain"
	if strings.HasSuffix(path, ".html") {
		contentType = "text/html"
	} else if strings.HasSuffix(path, ".css") {
		contentType = "text/css"
	} else if strings.HasSuffix(path, ".js") {
		contentType = "application/javascript"
	} else if strings.HasSuffix(path, ".png") {
		contentType = "image/png"
	} else if strings.HasSuffix(path, ".jpg") || strings.HasSuffix(path, ".jpeg") {
		contentType = "image/jpeg"
	}

	content, err := os.ReadFile("." + path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Write(content)
}

// ==================== MAIN ====================

func main() {
	// Initialize database
	if err := initDB(); err != nil {
		log.Printf("Warning: Database initialization failed: %v", err)
	}

	// Get base URL
	baseURL = os.Getenv("BASE_URL")

	// Set up router
	r := mux.NewRouter()

	// Auth routes
	r.HandleFunc("/auth/discord", handleDiscordLogin).Methods("GET")
	r.HandleFunc("/auth/discord/callback", handleDiscordCallback).Methods("GET")
	r.HandleFunc("/auth/me", handleAuthMe).Methods("GET")
	r.HandleFunc("/auth/logout", handleLogout).Methods("POST")

	// API routes
	r.HandleFunc("/api/teams", handleGetTeams).Methods("GET")
	r.HandleFunc("/api/weeks", handleGetWeeks).Methods("GET")
	r.HandleFunc("/api/availability", handleGetAllAvailability).Methods("GET")
	r.HandleFunc("/api/availability/{teamId}/{weekId}", handleGetAvailability).Methods("GET")
	r.HandleFunc("/api/availability", handleSetAvailability).Methods("POST")
	r.HandleFunc("/api/team-availability/{weekId}", handleGetTeamWeekAvailability).Methods("GET")
	r.HandleFunc("/api/link-player", handleLinkPlayer).Methods("POST")
	r.HandleFunc("/api/webhook", handleGetWebhook).Methods("GET")
	r.HandleFunc("/api/webhook", handleSetWebhook).Methods("POST")
	r.HandleFunc("/api/announce", handleAnnounceWeek).Methods("POST")

	// Admin routes
	r.HandleFunc("/api/admin/import-teams", handleImportTeams).Methods("POST")
	r.HandleFunc("/api/admin/import-weeks", handleImportWeeks).Methods("POST")
	r.HandleFunc("/api/admin/set-admin", handleSetAdmin).Methods("POST")

	// Static files
	r.PathPrefix("/").HandlerFunc(serveStatic)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Printf("GenX League Calendar starting on port %s", port)
	log.Fatal(http.ListenAndServe(":"+port, r))
}
