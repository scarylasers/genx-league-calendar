package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/lib/pq"
	"golang.org/x/net/html"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
)

// ==================== CONFIG ====================

var (
	db                  *sql.DB
	discordClientID     = os.Getenv("DISCORD_CLIENT_ID")
	discordClientSecret = os.Getenv("DISCORD_CLIENT_SECRET")
	discordBotToken     = os.Getenv("DISCORD_BOT_TOKEN")
	sessionSecret       = os.Getenv("SESSION_SECRET")
	baseURL             = os.Getenv("BASE_URL")
	googleSheetsID      = os.Getenv("GOOGLE_SHEETS_ID")
	googleServiceJSON   = os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON")
	sheetsService       *sheets.Service
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

type Sub struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DiscordID    string `json:"discordId,omitempty"`
	DiscordName  string `json:"discordName,omitempty"`
	CompRank     int    `json:"compRank"`
	Available    bool   `json:"available"`
	Notes        string `json:"notes,omitempty"`
	LastActive   string `json:"lastActive,omitempty"`
}

type Tier struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Min  int    `json:"min"`
	Max  int    `json:"max"`
}

type RegisteredPlayer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	CompRank int    `json:"compRank"`
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

	// Add search_path to connection string for genx_league schema
	if strings.Contains(dbURL, "?") {
		dbURL += "&search_path=genx_league"
	} else {
		dbURL += "?search_path=genx_league"
	}

	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		return err
	}

	// Create schema for genx_league to keep tables separate from hopzle
	if _, err := db.Exec(`CREATE SCHEMA IF NOT EXISTS genx_league`); err != nil {
		return fmt.Errorf("failed to create schema: %v", err)
	}

	// Create tables (will be in genx_league schema due to search_path)
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
		`CREATE TABLE IF NOT EXISTS subs (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			discord_id TEXT,
			discord_name TEXT,
			comp_rank INTEGER DEFAULT 0,
			available BOOLEAN DEFAULT TRUE,
			notes TEXT,
			last_active TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS tiers (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			min_rank INTEGER NOT NULL,
			max_rank INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS registered_players (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			comp_rank INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS sub_assignments (
			id SERIAL PRIMARY KEY,
			sub_id TEXT NOT NULL,
			week_id TEXT NOT NULL,
			team_id TEXT NOT NULL,
			assigned_by TEXT,
			assigned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(sub_id, week_id)
		)`,
	}

	for _, table := range tables {
		if _, err := db.Exec(table); err != nil {
			return fmt.Errorf("failed to create table: %v", err)
		}
	}

	log.Println("Database initialized with genx_league schema")
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

// ==================== GOOGLE SHEETS ====================

func initSheetsService() error {
	if googleServiceJSON == "" {
		log.Println("Google Sheets not configured (no service account JSON)")
		return nil
	}

	ctx := context.Background()
	srv, err := sheets.NewService(ctx, option.WithCredentialsJSON([]byte(googleServiceJSON)))
	if err != nil {
		return fmt.Errorf("failed to create sheets service: %v", err)
	}
	sheetsService = srv
	log.Println("Google Sheets service initialized")
	return nil
}

func syncFromSheets() error {
	if sheetsService == nil || googleSheetsID == "" {
		return fmt.Errorf("Google Sheets not configured")
	}

	log.Println("Starting sync from Google Sheets...")

	// Sync Teams
	if err := syncTeamsFromSheets(); err != nil {
		log.Printf("Warning: Failed to sync teams: %v", err)
	}

	// Sync Schedule
	if err := syncScheduleFromSheets(); err != nil {
		log.Printf("Warning: Failed to sync schedule: %v", err)
	}

	// Update last sync time
	setSetting("last_sync", time.Now().UTC().Format(time.RFC3339))

	log.Println("Sync from Google Sheets complete")
	return nil
}

func syncTeamsFromSheets() error {
	// Read Teams sheet - expecting columns: Team Name, Player 1-5, Sub 1-4
	resp, err := sheetsService.Spreadsheets.Values.Get(googleSheetsID, "Teams!A:J").Do()
	if err != nil {
		return fmt.Errorf("failed to read Teams sheet: %v", err)
	}

	if len(resp.Values) < 2 {
		return fmt.Errorf("Teams sheet is empty or missing header")
	}

	// Skip header row
	for i, row := range resp.Values[1:] {
		if len(row) < 1 {
			continue
		}

		teamName := strings.TrimSpace(fmt.Sprintf("%v", row[0]))
		if teamName == "" {
			continue
		}

		// Extract players (columns B-F, indices 1-5)
		players := []string{}
		for j := 1; j <= 5 && j < len(row); j++ {
			if p := strings.TrimSpace(fmt.Sprintf("%v", row[j])); p != "" {
				players = append(players, p)
			}
		}

		// Extract subs (columns G-J, indices 6-9)
		subs := []string{}
		for j := 6; j <= 9 && j < len(row); j++ {
			if s := strings.TrimSpace(fmt.Sprintf("%v", row[j])); s != "" {
				subs = append(subs, s)
			}
		}

		team := Team{
			ID:      fmt.Sprintf("team-%d", i+1),
			Name:    teamName,
			Players: players,
			Subs:    subs,
		}

		if err := saveTeam(team); err != nil {
			log.Printf("Failed to save team %s: %v", teamName, err)
		}
	}

	return nil
}

func syncScheduleFromSheets() error {
	// Read Schedule sheet - expecting columns: Week, Lobby, Team 1-7
	resp, err := sheetsService.Spreadsheets.Values.Get(googleSheetsID, "Schedule!A:I").Do()
	if err != nil {
		return fmt.Errorf("failed to read Schedule sheet: %v", err)
	}

	if len(resp.Values) < 2 {
		return fmt.Errorf("Schedule sheet is empty or missing header")
	}

	// Group rows by week
	weekLobbies := make(map[string][]Lobby)
	weekNumbers := make(map[string]int)
	weekNum := 0

	for _, row := range resp.Values[1:] {
		if len(row) < 2 {
			continue
		}

		weekName := strings.TrimSpace(fmt.Sprintf("%v", row[0]))
		lobbyName := strings.TrimSpace(fmt.Sprintf("%v", row[1]))

		if weekName == "" || lobbyName == "" {
			continue
		}

		// Track week number
		if _, exists := weekNumbers[weekName]; !exists {
			weekNum++
			weekNumbers[weekName] = weekNum
		}

		// Extract teams in this lobby (columns C onwards)
		teams := []string{}
		for j := 2; j < len(row); j++ {
			if t := strings.TrimSpace(fmt.Sprintf("%v", row[j])); t != "" {
				teams = append(teams, t)
			}
		}

		lobby := Lobby{
			Name:  lobbyName,
			Teams: teams,
		}

		weekLobbies[weekName] = append(weekLobbies[weekName], lobby)
	}

	// Save weeks
	for weekName, lobbies := range weekLobbies {
		weekID := strings.ToLower(strings.ReplaceAll(weekName, " ", "-"))
		week := Week{
			ID:      weekID,
			Number:  weekNumbers[weekName],
			Name:    weekName,
			Lobbies: lobbies,
		}

		if err := saveWeek(week); err != nil {
			log.Printf("Failed to save week %s: %v", weekName, err)
		}
	}

	return nil
}

// Auto-sync goroutine
func startAutoSync(interval time.Duration) {
	go func() {
		// Initial sync after startup
		time.Sleep(10 * time.Second)

		// Try Google Sheets API first, then published URL
		if sheetsService != nil && googleSheetsID != "" {
			syncFromSheets()
		} else {
			// Try published URL
			if err := syncFromPublishedURL(); err != nil {
				log.Printf("Auto-sync from published URL failed: %v", err)
			}
		}

		ticker := time.NewTicker(interval)
		for range ticker.C {
			if sheetsService != nil && googleSheetsID != "" {
				syncFromSheets()
			} else {
				if err := syncFromPublishedURL(); err != nil {
					log.Printf("Auto-sync from published URL failed: %v", err)
				}
			}
		}
	}()

	log.Printf("Auto-sync enabled (every %v)", interval)
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

func deleteTeam(id string) error {
	_, err := db.Exec("DELETE FROM teams WHERE id = $1", id)
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

// ==================== SUB POOL FUNCTIONS ====================

func getAllSubs() ([]Sub, error) {
	rows, err := db.Query("SELECT id, name, discord_id, discord_name, comp_rank, available, notes, last_active FROM subs ORDER BY comp_rank DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []Sub
	for rows.Next() {
		var s Sub
		var discordID, discordName, notes sql.NullString
		var lastActive sql.NullTime
		if err := rows.Scan(&s.ID, &s.Name, &discordID, &discordName, &s.CompRank, &s.Available, &notes, &lastActive); err != nil {
			continue
		}
		s.DiscordID = discordID.String
		s.DiscordName = discordName.String
		s.Notes = notes.String
		if lastActive.Valid {
			s.LastActive = lastActive.Time.Format(time.RFC3339)
		}
		subs = append(subs, s)
	}
	return subs, nil
}

func getSubByID(id string) (*Sub, error) {
	var s Sub
	var discordID, discordName, notes sql.NullString
	var lastActive sql.NullTime
	err := db.QueryRow(`
		SELECT id, name, discord_id, discord_name, comp_rank, available, notes, last_active
		FROM subs WHERE id = $1
	`, id).Scan(&s.ID, &s.Name, &discordID, &discordName, &s.CompRank, &s.Available, &notes, &lastActive)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.DiscordID = discordID.String
	s.DiscordName = discordName.String
	s.Notes = notes.String
	if lastActive.Valid {
		s.LastActive = lastActive.Time.Format(time.RFC3339)
	}
	return &s, nil
}

func getSubByDiscordID(discordID string) (*Sub, error) {
	var s Sub
	var dID, discordName, notes sql.NullString
	var lastActive sql.NullTime
	err := db.QueryRow(`
		SELECT id, name, discord_id, discord_name, comp_rank, available, notes, last_active
		FROM subs WHERE discord_id = $1
	`, discordID).Scan(&s.ID, &s.Name, &dID, &discordName, &s.CompRank, &s.Available, &notes, &lastActive)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.DiscordID = dID.String
	s.DiscordName = discordName.String
	s.Notes = notes.String
	if lastActive.Valid {
		s.LastActive = lastActive.Time.Format(time.RFC3339)
	}
	return &s, nil
}

func saveSub(s Sub) error {
	_, err := db.Exec(`
		INSERT INTO subs (id, name, discord_id, discord_name, comp_rank, available, notes, last_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP)
		ON CONFLICT (id) DO UPDATE SET
			name = $2, discord_id = $3, discord_name = $4, comp_rank = $5, available = $6, notes = $7, last_active = CURRENT_TIMESTAMP
	`, s.ID, s.Name, s.DiscordID, s.DiscordName, s.CompRank, s.Available, s.Notes)
	return err
}

// ==================== SUB ASSIGNMENT FUNCTIONS ====================

type SubAssignment struct {
	ID         int    `json:"id"`
	SubID      string `json:"subId"`
	SubName    string `json:"subName,omitempty"`
	WeekID     string `json:"weekId"`
	TeamID     string `json:"teamId"`
	TeamName   string `json:"teamName,omitempty"`
	AssignedBy string `json:"assignedBy,omitempty"`
	AssignedAt string `json:"assignedAt,omitempty"`
}

// getSubAssignmentsForWeek returns all sub assignments for a specific week
func getSubAssignmentsForWeek(weekID string) ([]SubAssignment, error) {
	rows, err := db.Query(`
		SELECT sa.id, sa.sub_id, sa.week_id, sa.team_id, sa.assigned_by, sa.assigned_at,
			   s.name as sub_name, t.name as team_name
		FROM sub_assignments sa
		LEFT JOIN subs s ON sa.sub_id = s.id
		LEFT JOIN teams t ON sa.team_id = t.id OR sa.team_id = t.name
		WHERE sa.week_id = $1
		ORDER BY sa.assigned_at DESC
	`, weekID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assignments []SubAssignment
	for rows.Next() {
		var a SubAssignment
		var assignedBy, assignedAt, subName, teamName sql.NullString
		if err := rows.Scan(&a.ID, &a.SubID, &a.WeekID, &a.TeamID, &assignedBy, &assignedAt, &subName, &teamName); err != nil {
			continue
		}
		a.AssignedBy = assignedBy.String
		a.AssignedAt = assignedAt.String
		a.SubName = subName.String
		a.TeamName = teamName.String
		assignments = append(assignments, a)
	}
	return assignments, nil
}

// getSubAssignmentsForTeamWeek returns sub assignments for a specific team in a specific week
func getSubAssignmentsForTeamWeek(teamID, weekID string) ([]SubAssignment, error) {
	rows, err := db.Query(`
		SELECT sa.id, sa.sub_id, sa.week_id, sa.team_id, sa.assigned_by, sa.assigned_at,
			   s.name as sub_name
		FROM sub_assignments sa
		LEFT JOIN subs s ON sa.sub_id = s.id
		WHERE (sa.team_id = $1 OR sa.team_id = $2) AND sa.week_id = $3
		ORDER BY sa.assigned_at DESC
	`, teamID, teamID, weekID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var assignments []SubAssignment
	for rows.Next() {
		var a SubAssignment
		var assignedBy, assignedAt, subName sql.NullString
		if err := rows.Scan(&a.ID, &a.SubID, &a.WeekID, &a.TeamID, &assignedBy, &assignedAt, &subName); err != nil {
			continue
		}
		a.AssignedBy = assignedBy.String
		a.AssignedAt = assignedAt.String
		a.SubName = subName.String
		assignments = append(assignments, a)
	}
	return assignments, nil
}

// isSubAssignedToWeek checks if a sub is already assigned to any team for a specific week
func isSubAssignedToWeek(subID, weekID string) (bool, string) {
	var teamID string
	err := db.QueryRow(`
		SELECT team_id FROM sub_assignments WHERE sub_id = $1 AND week_id = $2
	`, subID, weekID).Scan(&teamID)
	if err == sql.ErrNoRows {
		return false, ""
	}
	if err != nil {
		return false, ""
	}
	return true, teamID
}

// assignSubToTeam assigns a sub to a team for a specific week
func assignSubToTeam(subID, weekID, teamID, assignedBy string) error {
	_, err := db.Exec(`
		INSERT INTO sub_assignments (sub_id, week_id, team_id, assigned_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (sub_id, week_id) DO UPDATE SET
			team_id = $3, assigned_by = $4, assigned_at = CURRENT_TIMESTAMP
	`, subID, weekID, teamID, assignedBy)
	return err
}

// unassignSubFromTeam removes a sub assignment
func unassignSubFromTeam(subID, weekID, teamID string) error {
	_, err := db.Exec(`
		DELETE FROM sub_assignments WHERE sub_id = $1 AND week_id = $2 AND team_id = $3
	`, subID, weekID, teamID)
	return err
}

// getAssignedSubIDsForWeek returns a set of sub IDs that are assigned to any team in a specific week
func getAssignedSubIDsForWeek(weekID string) (map[string]bool, error) {
	rows, err := db.Query("SELECT sub_id FROM sub_assignments WHERE week_id = $1", weekID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	assigned := make(map[string]bool)
	for rows.Next() {
		var subID string
		if err := rows.Scan(&subID); err != nil {
			continue
		}
		assigned[subID] = true
	}
	return assigned, nil
}

// ==================== TIER FUNCTIONS ====================

func getAllTiers() ([]Tier, error) {
	rows, err := db.Query("SELECT id, name, min_rank, max_rank FROM tiers ORDER BY max_rank DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tiers []Tier
	for rows.Next() {
		var t Tier
		if err := rows.Scan(&t.ID, &t.Name, &t.Min, &t.Max); err != nil {
			continue
		}
		tiers = append(tiers, t)
	}
	return tiers, nil
}

func saveTier(t Tier) error {
	_, err := db.Exec(`
		INSERT INTO tiers (id, name, min_rank, max_rank) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET name = $2, min_rank = $3, max_rank = $4
	`, t.ID, t.Name, t.Min, t.Max)
	return err
}

func deleteTier(id string) error {
	_, err := db.Exec("DELETE FROM tiers WHERE id = $1", id)
	return err
}

func getTierForRank(rank int, tiers []Tier) *Tier {
	for _, t := range tiers {
		if rank >= t.Min && rank <= t.Max {
			return &t
		}
	}
	return nil
}

func deleteSub(id string) error {
	_, err := db.Exec("DELETE FROM subs WHERE id = $1", id)
	return err
}

// ==================== REGISTERED PLAYERS FUNCTIONS ====================

func getAllRegisteredPlayers() ([]RegisteredPlayer, error) {
	rows, err := db.Query("SELECT id, name, comp_rank FROM registered_players ORDER BY comp_rank DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var players []RegisteredPlayer
	for rows.Next() {
		var p RegisteredPlayer
		if err := rows.Scan(&p.ID, &p.Name, &p.CompRank); err != nil {
			continue
		}
		players = append(players, p)
	}
	return players, nil
}

func getRegisteredPlayerByName(name string) (*RegisteredPlayer, error) {
	var p RegisteredPlayer
	err := db.QueryRow("SELECT id, name, comp_rank FROM registered_players WHERE LOWER(name) = LOWER($1)", name).
		Scan(&p.ID, &p.Name, &p.CompRank)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func saveRegisteredPlayer(p RegisteredPlayer) error {
	_, err := db.Exec(`
		INSERT INTO registered_players (id, name, comp_rank) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET name = $2, comp_rank = $3
	`, p.ID, p.Name, p.CompRank)
	return err
}

func clearRegisteredPlayers() error {
	_, err := db.Exec("DELETE FROM registered_players")
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

func getAllUsers() ([]User, error) {
	rows, err := db.Query(`
		SELECT discord_id, username, display_name, avatar, team_id, player_name, is_admin, is_manager
		FROM users ORDER BY display_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var teamID, playerName sql.NullString
		if err := rows.Scan(&u.DiscordID, &u.Username, &u.DisplayName, &u.Avatar, &teamID, &playerName, &u.IsAdmin, &u.IsManager); err != nil {
			continue
		}
		u.TeamID = teamID.String
		u.PlayerName = playerName.String
		users = append(users, u)
	}
	return users, nil
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

// ==================== SUB POOL HANDLERS ====================

func handleGetSubs(w http.ResponseWriter, r *http.Request) {
	subs, err := getAllSubs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if subs == nil {
		subs = []Sub{}
	}
	writeJSON(w, http.StatusOK, subs)
}

func handleRegisterAsSub(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Must be logged in")
		return
	}

	var body struct {
		Name  string `json:"name"`
		Notes string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Use player name if linked, otherwise use provided name or discord name
	name := body.Name
	if name == "" && session.PlayerName != "" {
		name = session.PlayerName
	}
	if name == "" {
		name = session.DisplayName
	}

	// Check if player is on a team roster - they cannot be a sub
	teamName := isPlayerOnTeam(name)
	if teamName != "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("You are registered on team '%s' and cannot join the sub pool. Only unrostered players can be subs.", teamName))
		return
	}

	// Look up CR from registered players - users cannot set their own CR
	registeredPlayer, err := getRegisteredPlayerByName(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to lookup player")
		return
	}
	if registeredPlayer == nil {
		writeError(w, http.StatusBadRequest, "You must be registered via stat-bot before joining the sub pool. Contact a league admin.")
		return
	}

	sub := Sub{
		ID:          "sub-" + session.DiscordID,
		Name:        name,
		DiscordID:   session.DiscordID,
		DiscordName: session.Username,
		CompRank:    registeredPlayer.CompRank,
		Available:   true,
		Notes:       body.Notes,
	}

	if err := saveSub(sub); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"sub":     sub,
	})
}

// isPlayerOnTeam checks if a player name is on any team roster (players or subs)
// Returns the team name if found, empty string if not
func isPlayerOnTeam(playerName string) string {
	return isPlayerOnTeamExcluding(playerName, "")
}

// isPlayerOnTeamExcluding checks if a player is on any team roster, excluding a specific team
// This is used when updating a team so its own players don't count as duplicates
func isPlayerOnTeamExcluding(playerName string, excludeTeamID string) string {
	teams, err := getAllTeams()
	if err != nil {
		return ""
	}

	playerNameLower := strings.ToLower(playerName)
	for _, team := range teams {
		// Skip the excluded team
		if excludeTeamID != "" && (team.ID == excludeTeamID || team.Name == excludeTeamID) {
			continue
		}
		// Check main roster
		for _, p := range team.Players {
			if strings.ToLower(p) == playerNameLower {
				return team.Name
			}
		}
		// Check team subs (these are team-specific subs, not the sub pool)
		for _, s := range team.Subs {
			if strings.ToLower(s) == playerNameLower {
				return team.Name
			}
		}
	}
	return ""
}

// validateRosterPlayers checks if any players in the list are already on another team
// Returns the first duplicate found with the team name, or empty strings if all clear
func validateRosterPlayers(players []string, subs []string, excludeTeamID string) (string, string) {
	// Check all players
	for _, player := range players {
		if player == "" {
			continue
		}
		existingTeam := isPlayerOnTeamExcluding(player, excludeTeamID)
		if existingTeam != "" {
			return player, existingTeam
		}
	}
	// Check all subs
	for _, sub := range subs {
		if sub == "" {
			continue
		}
		existingTeam := isPlayerOnTeamExcluding(sub, excludeTeamID)
		if existingTeam != "" {
			return sub, existingTeam
		}
	}
	return "", ""
}

func handleUpdateSubAvailability(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Must be logged in")
		return
	}

	var body struct {
		Available bool `json:"available"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Find sub by discord ID
	sub, err := getSubByDiscordID(session.DiscordID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sub == nil {
		writeError(w, http.StatusNotFound, "You are not registered as a sub")
		return
	}

	sub.Available = body.Available
	if err := saveSub(*sub); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func handleUnregisterSub(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Must be logged in")
		return
	}

	// Find and delete sub by discord ID
	sub, _ := getSubByDiscordID(session.DiscordID)
	if sub == nil {
		writeError(w, http.StatusNotFound, "You are not registered as a sub")
		return
	}

	if err := deleteSub(sub.ID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func handleImportSubs(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var body struct {
		Data string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Parse the stat-bot format:
	// (CR)    IGN
	// e.g., "(163)    F(xyz)" or "(159)    VDT.ElephantGhost"
	lines := strings.Split(body.Data, "\n")
	imported := 0

	// Regex to match: (number) followed by whitespace and then the name
	crPattern := regexp.MustCompile(`^\((\d+)\)\s+(.+)$`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := crPattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			// Try old format as fallback: "Name X Rank: ### Comp Rank: ###"
			xRankIdx := strings.Index(line, " X Rank: ")
			if xRankIdx != -1 {
				name := strings.TrimSpace(line[:xRankIdx])
				rest := line[xRankIdx+9:]
				compRankIdx := strings.Index(rest, " Comp Rank: ")
				if compRankIdx != -1 {
					compRankStr := strings.TrimSpace(rest[compRankIdx+12:])
					compRank, _ := strconv.Atoi(compRankStr)
					subID := "sub-" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, " ", "-"), ".", "-"))
					sub := Sub{
						ID:        subID,
						Name:      name,
						CompRank:  compRank,
						Available: true,
					}
					if err := saveSub(sub); err != nil {
						log.Printf("Failed to save sub %s: %v", name, err)
						continue
					}
					imported++
				}
			}
			continue
		}

		compRank, _ := strconv.Atoi(matches[1])
		name := strings.TrimSpace(matches[2])

		// Create sub ID from name (lowercase, replace spaces and special chars)
		subID := "sub-" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, " ", "-"), ".", "-"))

		sub := Sub{
			ID:        subID,
			Name:      name,
			CompRank:  compRank,
			Available: true,
		}

		if err := saveSub(sub); err != nil {
			log.Printf("Failed to save sub %s: %v", name, err)
			continue
		}
		imported++
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"imported": imported,
	})
}

func handleGetRegisteredPlayers(w http.ResponseWriter, r *http.Request) {
	players, err := getAllRegisteredPlayers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if players == nil {
		players = []RegisteredPlayer{}
	}
	writeJSON(w, http.StatusOK, players)
}

func handleImportRegisteredPlayers(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var body struct {
		Data    string `json:"data"`
		Replace bool   `json:"replace"` // If true, clear existing players first
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Clear existing if requested
	if body.Replace {
		if err := clearRegisteredPlayers(); err != nil {
			log.Printf("Failed to clear registered players: %v", err)
		}
	}

	// Parse the stat-bot format: (CR)    IGN
	lines := strings.Split(body.Data, "\n")
	imported := 0

	crPattern := regexp.MustCompile(`^\((\d+)\)\s+(.+)$`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		matches := crPattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			// Try old format as fallback
			xRankIdx := strings.Index(line, " X Rank: ")
			if xRankIdx != -1 {
				name := strings.TrimSpace(line[:xRankIdx])
				rest := line[xRankIdx+9:]
				compRankIdx := strings.Index(rest, " Comp Rank: ")
				if compRankIdx != -1 {
					compRankStr := strings.TrimSpace(rest[compRankIdx+12:])
					compRank, _ := strconv.Atoi(compRankStr)
					playerID := "player-" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, " ", "-"), ".", "-"))
					player := RegisteredPlayer{
						ID:       playerID,
						Name:     name,
						CompRank: compRank,
					}
					if err := saveRegisteredPlayer(player); err != nil {
						log.Printf("Failed to save player %s: %v", name, err)
						continue
					}
					imported++
				}
			}
			continue
		}

		compRank, _ := strconv.Atoi(matches[1])
		name := strings.TrimSpace(matches[2])

		playerID := "player-" + strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, " ", "-"), ".", "-"))

		player := RegisteredPlayer{
			ID:       playerID,
			Name:     name,
			CompRank: compRank,
		}

		if err := saveRegisteredPlayer(player); err != nil {
			log.Printf("Failed to save player %s: %v", name, err)
			continue
		}
		imported++
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"imported": imported,
	})
}

func handleGetPlayerCR(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "Name parameter required")
		return
	}

	player, err := getRegisteredPlayerByName(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Check if player is on a team
	teamName := isPlayerOnTeam(name)

	if player == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"found":  false,
			"onTeam": teamName != "",
			"team":   teamName,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"found":    true,
		"name":     player.Name,
		"compRank": player.CompRank,
		"onTeam":   teamName != "",
		"team":     teamName,
	})
}

func handleGetSubRules(w http.ResponseWriter, r *http.Request) {
	tiers, err := getAllTiers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tiers == nil {
		tiers = []Tier{}
	}

	// Get simple threshold settings
	maxOverageStr, _ := getSetting("sub_max_overage")
	equalFloorStr, _ := getSetting("sub_equal_floor")

	maxOverage := 0
	equalFloor := 0
	if maxOverageStr != "" {
		maxOverage, _ = strconv.Atoi(maxOverageStr)
	}
	if equalFloorStr != "" {
		equalFloor, _ = strconv.Atoi(equalFloorStr)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tiers":      tiers,
		"maxOverage": maxOverage,
		"equalFloor": equalFloor,
	})
}

func handleSetSubRules(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var body struct {
		Tiers      []Tier `json:"tiers"`
		MaxOverage int    `json:"maxOverage"`
		EqualFloor int    `json:"equalFloor"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Save simple threshold settings
	setSetting("sub_max_overage", strconv.Itoa(body.MaxOverage))
	setSetting("sub_equal_floor", strconv.Itoa(body.EqualFloor))

	// Delete all existing tiers and save new ones
	db.Exec("DELETE FROM tiers")

	for i, tier := range body.Tiers {
		if tier.ID == "" {
			tier.ID = fmt.Sprintf("tier-%d", i+1)
		}
		if tier.Name == "" {
			tier.Name = fmt.Sprintf("Tier %d", i+1)
		}
		saveTier(tier)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func handleGetEligibleSubs(w http.ResponseWriter, r *http.Request) {
	outgoingCRStr := r.URL.Query().Get("cr")
	outgoingCR, _ := strconv.Atoi(outgoingCRStr)
	weekID := r.URL.Query().Get("weekId")

	// Get tiers
	tiers, _ := getAllTiers()

	// Get threshold settings
	maxOverageStr, _ := getSetting("sub_max_overage")
	equalFloorStr, _ := getSetting("sub_equal_floor")
	maxOverage := 0
	equalFloor := 0
	if maxOverageStr != "" {
		maxOverage, _ = strconv.Atoi(maxOverageStr)
	}
	if equalFloorStr != "" {
		equalFloor, _ = strconv.Atoi(equalFloorStr)
	}

	// Get all available subs
	allSubs, err := getAllSubs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Get subs already assigned to this week (if weekId provided)
	var assignedSubIDs map[string]bool
	if weekID != "" {
		assignedSubIDs, _ = getAssignedSubIDsForWeek(weekID)
	}

	// Find which tier the outgoing player is in
	outgoingTier := getTierForRank(outgoingCR, tiers)

	// Filter eligible subs
	eligible := []Sub{}
	for _, sub := range allSubs {
		if !sub.Available {
			continue
		}

		// Skip subs already assigned to another team this week
		if assignedSubIDs != nil && assignedSubIDs[sub.ID] {
			continue
		}

		// Check eligibility based on rules
		if isSubEligible(sub.CompRank, outgoingCR, outgoingTier, tiers, maxOverage, equalFloor) {
			eligible = append(eligible, sub)
		}
	}

	var tierName string
	if outgoingTier != nil {
		tierName = outgoingTier.Name
	}

	// Build explanation of why subs are eligible
	var reason string
	if outgoingCR <= equalFloor && equalFloor > 0 {
		reason = fmt.Sprintf("Below equal floor (%d) - all low CR players eligible", equalFloor)
	} else if outgoingTier != nil {
		reason = fmt.Sprintf("In %s tier - same-tier subs allowed", tierName)
	} else if maxOverage > 0 {
		reason = fmt.Sprintf("CR up to %d allowed (outgoing %d + %d overage)", outgoingCR+maxOverage, outgoingCR, maxOverage)
	} else {
		reason = fmt.Sprintf("CR must be ≤ %d", outgoingCR)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"eligible":   eligible,
		"outgoingCR": outgoingCR,
		"tier":       tierName,
		"tiers":      tiers,
		"maxOverage": maxOverage,
		"equalFloor": equalFloor,
		"reason":     reason,
	})
}

func isSubEligible(subCR, outgoingCR int, outgoingTier *Tier, tiers []Tier, maxOverage, equalFloor int) bool {
	// Rule 1: If both players are at or below the equal floor, they can sub for each other
	if equalFloor > 0 && outgoingCR <= equalFloor && subCR <= equalFloor {
		return true
	}

	// Rule 2: If both are in the same tier, they can sub for each other
	subTier := getTierForRank(subCR, tiers)
	if outgoingTier != nil && subTier != nil && outgoingTier.ID == subTier.ID {
		return true
	}

	// Rule 3: Sub CR must be <= outgoing CR + maxOverage
	return subCR <= outgoingCR+maxOverage
}

// ==================== SUB ASSIGNMENT HANDLERS ====================

// handleAssignSub assigns a sub to a team for a specific week
func handleAssignSub(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Login required")
		return
	}

	// Must be team manager or admin
	if !session.IsAdmin && !session.IsManager {
		writeError(w, http.StatusForbidden, "Team manager or admin access required")
		return
	}

	var body struct {
		SubID  string `json:"subId"`
		WeekID string `json:"weekId"`
		TeamID string `json:"teamId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if body.SubID == "" || body.WeekID == "" || body.TeamID == "" {
		writeError(w, http.StatusBadRequest, "subId, weekId, and teamId are required")
		return
	}

	// If not admin, must be assigning to their own team
	if !session.IsAdmin && session.TeamID != body.TeamID {
		writeError(w, http.StatusForbidden, "You can only assign subs to your own team")
		return
	}

	// Check if sub exists
	sub, err := getSubByID(body.SubID)
	if err != nil || sub == nil {
		writeError(w, http.StatusNotFound, "Sub not found")
		return
	}

	// Check if sub is available
	if !sub.Available {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%s is currently marked as unavailable", sub.Name))
		return
	}

	// Check if sub is already assigned to another team this week
	alreadyAssigned, existingTeamID := isSubAssignedToWeek(body.SubID, body.WeekID)
	if alreadyAssigned && existingTeamID != body.TeamID {
		// Get team name for better error message
		existingTeam, _ := getTeamByID(existingTeamID)
		teamName := existingTeamID
		if existingTeam != nil {
			teamName = existingTeam.Name
		}
		writeError(w, http.StatusConflict, fmt.Sprintf("%s is already assigned to %s for this week", sub.Name, teamName))
		return
	}

	// Assign the sub
	if err := assignSubToTeam(body.SubID, body.WeekID, body.TeamID, session.DiscordID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"subName": sub.Name,
		"weekId":  body.WeekID,
		"teamId":  body.TeamID,
	})
}

// handleUnassignSub removes a sub assignment
func handleUnassignSub(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil {
		writeError(w, http.StatusUnauthorized, "Login required")
		return
	}

	// Must be team manager or admin
	if !session.IsAdmin && !session.IsManager {
		writeError(w, http.StatusForbidden, "Team manager or admin access required")
		return
	}

	var body struct {
		SubID  string `json:"subId"`
		WeekID string `json:"weekId"`
		TeamID string `json:"teamId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// If not admin, must be unassigning from their own team
	if !session.IsAdmin && session.TeamID != body.TeamID {
		writeError(w, http.StatusForbidden, "You can only unassign subs from your own team")
		return
	}

	if err := unassignSubFromTeam(body.SubID, body.WeekID, body.TeamID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// handleGetSubAssignments returns sub assignments for a week or team
func handleGetSubAssignments(w http.ResponseWriter, r *http.Request) {
	weekID := r.URL.Query().Get("weekId")
	teamID := r.URL.Query().Get("teamId")

	var assignments []SubAssignment
	var err error

	if weekID != "" && teamID != "" {
		assignments, err = getSubAssignmentsForTeamWeek(teamID, weekID)
	} else if weekID != "" {
		assignments, err = getSubAssignmentsForWeek(weekID)
	} else {
		writeError(w, http.StatusBadRequest, "weekId is required")
		return
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if assignments == nil {
		assignments = []SubAssignment{}
	}

	writeJSON(w, http.StatusOK, assignments)
}

// ==================== ADMIN HANDLERS ====================

// Team management
func handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	vars := mux.Vars(r)
	teamID := vars["teamId"]

	var body struct {
		Name    string   `json:"name"`
		Players []string `json:"players"`
		Subs    []string `json:"subs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Get existing team
	team, err := getTeamByID(teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if team == nil {
		writeError(w, http.StatusNotFound, "Team not found")
		return
	}

	// Update fields
	if body.Name != "" {
		team.Name = body.Name
	}
	if body.Players != nil {
		team.Players = body.Players
	}
	if body.Subs != nil {
		team.Subs = body.Subs
	}

	// Check for duplicate players across rosters (excluding this team)
	duplicatePlayer, existingTeam := validateRosterPlayers(team.Players, team.Subs, teamID)
	if duplicatePlayer != "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Player '%s' is already on team '%s'", duplicatePlayer, existingTeam))
		return
	}

	if err := saveTeam(*team); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"team":    team,
	})
}

func handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var body struct {
		Name    string   `json:"name"`
		Players []string `json:"players"`
		Subs    []string `json:"subs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "Team name is required")
		return
	}

	// Check if team name already exists
	existing, _ := getTeamByName(body.Name)
	if existing != nil {
		writeError(w, http.StatusBadRequest, "A team with this name already exists")
		return
	}

	// Check for duplicate players across rosters
	if body.Players == nil {
		body.Players = []string{}
	}
	if body.Subs == nil {
		body.Subs = []string{}
	}

	duplicatePlayer, existingTeam := validateRosterPlayers(body.Players, body.Subs, "")
	if duplicatePlayer != "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Player '%s' is already on team '%s'", duplicatePlayer, existingTeam))
		return
	}

	team := Team{
		ID:      "team-" + strings.ToLower(strings.ReplaceAll(body.Name, " ", "-")),
		Name:    body.Name,
		Players: body.Players,
		Subs:    body.Subs,
	}

	if err := saveTeam(team); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"team":    team,
	})
}

func handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	vars := mux.Vars(r)
	teamID := vars["teamId"]

	team, _ := getTeamByID(teamID)
	if team == nil {
		writeError(w, http.StatusNotFound, "Team not found")
		return
	}

	if err := deleteTeam(teamID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"teamName": team.Name,
	})
}

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

func handleGetUsers(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	users, err := getAllUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if users == nil {
		users = []User{}
	}

	// Don't expose full user data, just what's needed for admin management
	type UserInfo struct {
		DiscordID   string `json:"discordId"`
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Avatar      string `json:"avatar"`
		TeamName    string `json:"teamName"`
		PlayerName  string `json:"playerName"`
		IsAdmin     bool   `json:"isAdmin"`
	}

	var userInfos []UserInfo
	for _, u := range users {
		userInfos = append(userInfos, UserInfo{
			DiscordID:   u.DiscordID,
			Username:    u.Username,
			DisplayName: u.DisplayName,
			Avatar:      u.Avatar,
			TeamName:    u.TeamID, // TeamID stores the team name
			PlayerName:  u.PlayerName,
			IsAdmin:     u.IsAdmin,
		})
	}

	writeJSON(w, http.StatusOK, userInfos)
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

	// Prevent removing your own admin access
	if body.DiscordID == session.DiscordID && !body.IsAdmin {
		writeError(w, http.StatusBadRequest, "You cannot remove your own admin access")
		return
	}

	user, _ := getUserByDiscordID(body.DiscordID)
	if user == nil {
		writeError(w, http.StatusNotFound, "User not found. They must log in at least once first.")
		return
	}

	user.IsAdmin = body.IsAdmin
	saveUser(*user)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":     true,
		"discordId":   user.DiscordID,
		"displayName": user.DisplayName,
		"isAdmin":     user.IsAdmin,
	})
}

func handleSync(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	if sheetsService == nil {
		writeError(w, http.StatusServiceUnavailable, "Google Sheets not configured")
		return
	}

	if err := syncFromSheets(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"syncedAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func handleGetSyncStatus(w http.ResponseWriter, r *http.Request) {
	lastSync, _ := getSetting("last_sync")
	configured := sheetsService != nil && googleSheetsID != ""
	publishedURL, _ := getSetting("published_sheet_url")

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configured":   configured,
		"lastSync":     lastSync,
		"sheetId":      googleSheetsID,
		"publishedUrl": publishedURL,
	})
}

// ==================== IMPORT FROM PUBLISHED URL ====================

func handleImportFromURL(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	if body.URL == "" {
		writeError(w, http.StatusBadRequest, "URL is required")
		return
	}

	// Save the URL for future reference
	setSetting("published_sheet_url", body.URL)

	// Extract the spreadsheet key from the URL
	// Format: https://docs.google.com/spreadsheets/d/e/XXXXX/pubhtml
	var spreadsheetKey string
	if strings.Contains(body.URL, "/d/e/") {
		re := regexp.MustCompile(`/d/e/([^/]+)`)
		matches := re.FindStringSubmatch(body.URL)
		if len(matches) > 1 {
			spreadsheetKey = matches[1]
		}
	} else if strings.Contains(body.URL, "/d/") {
		re := regexp.MustCompile(`/d/([^/]+)`)
		matches := re.FindStringSubmatch(body.URL)
		if len(matches) > 1 {
			spreadsheetKey = matches[1]
		}
	}

	if spreadsheetKey == "" {
		writeError(w, http.StatusBadRequest, "Could not extract spreadsheet ID from URL")
		return
	}

	// First, get the list of sheets from the pubhtml page
	sheetsInfo, err := fetchSheetsList(body.URL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch sheets list: "+err.Error())
		return
	}

	teamsImported := 0
	weeksImported := 0
	var importErrors []string

	// Find and import Teams sheet
	for sheetName, gid := range sheetsInfo {
		if strings.ToLower(sheetName) == "teams" {
			csvURL := fmt.Sprintf("https://docs.google.com/spreadsheets/d/e/%s/pub?gid=%s&single=true&output=csv", spreadsheetKey, gid)
			teams, err := importTeamsFromCSV(csvURL)
			if err != nil {
				importErrors = append(importErrors, "Teams: "+err.Error())
			} else {
				teamsImported = len(teams)
			}
			break
		}
	}

	// Find and import Week sheets (Week 1, Week 2, etc., SemiFinals, Finals)
	weekPattern := regexp.MustCompile(`(?i)^(week\s*\d+|semifinals?|finals?)$`)
	weekNum := 0
	for sheetName, gid := range sheetsInfo {
		if weekPattern.MatchString(sheetName) {
			weekNum++
			csvURL := fmt.Sprintf("https://docs.google.com/spreadsheets/d/e/%s/pub?gid=%s&single=true&output=csv", spreadsheetKey, gid)
			count, err := importWeekFromCSV(csvURL, sheetName, weekNum)
			if err != nil {
				importErrors = append(importErrors, sheetName+": "+err.Error())
			} else {
				weeksImported += count
			}
		}
	}

	// Update last sync time
	setSetting("last_sync", time.Now().UTC().Format(time.RFC3339))

	result := map[string]interface{}{
		"success":       len(importErrors) == 0,
		"teamsImported": teamsImported,
		"weeksImported": weeksImported,
		"sheetsFound":   len(sheetsInfo),
	}
	if len(importErrors) > 0 {
		result["errors"] = importErrors
	}

	writeJSON(w, http.StatusOK, result)
}

func fetchSheetsList(pubhtmlURL string) (map[string]string, error) {
	resp, err := http.Get(pubhtmlURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil, err
	}

	sheets := make(map[string]string)

	// Find sheet tabs - they're usually in <li> elements with sheet-menu-button class
	// or in a script that defines the sheets
	var findSheets func(*html.Node)
	findSheets = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "li" {
			var sheetName, gid string
			for _, attr := range n.Attr {
				if attr.Key == "id" && strings.HasPrefix(attr.Val, "sheet-button-") {
					gid = strings.TrimPrefix(attr.Val, "sheet-button-")
				}
			}
			// Get the text content
			if gid != "" {
				sheetName = getTextContent(n)
				if sheetName != "" {
					sheets[sheetName] = gid
				}
			}
		}
		// Also check for links with gid parameter
		if n.Type == html.ElementNode && n.Data == "a" {
			for _, attr := range n.Attr {
				if attr.Key == "href" && strings.Contains(attr.Val, "gid=") {
					re := regexp.MustCompile(`gid=(\d+)`)
					matches := re.FindStringSubmatch(attr.Val)
					if len(matches) > 1 {
						gid := matches[1]
						name := getTextContent(n)
						if name != "" && gid != "" {
							sheets[name] = gid
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findSheets(c)
		}
	}
	findSheets(doc)

	// If we didn't find sheets via HTML, try to parse from JavaScript
	if len(sheets) == 0 {
		bodyContent := getBodyContent(doc)
		// Look for patterns like "sheetNames": ["Teams", "Week 1", ...]
		re := regexp.MustCompile(`"([^"]+)":\s*(\d+)`)
		matches := re.FindAllStringSubmatch(bodyContent, -1)
		for _, m := range matches {
			if len(m) > 2 {
				sheets[m[1]] = m[2]
			}
		}
	}

	// Fallback: if still no sheets, try gid=0 for first sheet
	if len(sheets) == 0 {
		sheets["Sheet1"] = "0"
	}

	return sheets, nil
}

func getTextContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return strings.TrimSpace(n.Data)
	}
	var text string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		text += getTextContent(c)
	}
	return strings.TrimSpace(text)
}

func getBodyContent(n *html.Node) string {
	if n.Type == html.ElementNode && n.Data == "body" {
		return getTextContent(n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if content := getBodyContent(c); content != "" {
			return content
		}
	}
	return ""
}

func importTeamsFromCSV(csvURL string) ([]Team, error) {
	resp, err := http.Get(csvURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	reader := csv.NewReader(resp.Body)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("no data rows found")
	}

	var teams []Team
	for i, row := range records[1:] { // Skip header
		if len(row) < 1 || strings.TrimSpace(row[0]) == "" {
			continue
		}

		teamName := strings.TrimSpace(row[0])

		// Extract players (columns B-F, indices 1-5)
		players := []string{}
		for j := 1; j <= 5 && j < len(row); j++ {
			if p := strings.TrimSpace(row[j]); p != "" {
				players = append(players, p)
			}
		}

		// Extract subs (columns G-J, indices 6-9)
		subs := []string{}
		for j := 6; j <= 9 && j < len(row); j++ {
			if s := strings.TrimSpace(row[j]); s != "" {
				subs = append(subs, s)
			}
		}

		team := Team{
			ID:      fmt.Sprintf("team-%d", i+1),
			Name:    teamName,
			Players: players,
			Subs:    subs,
		}

		if err := saveTeam(team); err != nil {
			log.Printf("Failed to save team %s: %v", teamName, err)
		}
		teams = append(teams, team)
	}

	return teams, nil
}

func importWeekFromCSV(csvURL string, weekName string, weekNum int) (int, error) {
	resp, err := http.Get(csvURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	reader := csv.NewReader(resp.Body)
	records, err := reader.ReadAll()
	if err != nil {
		return 0, err
	}

	if len(records) < 2 {
		return 0, fmt.Errorf("no data rows found")
	}

	// Parse the week sheet - expecting Lobby as rows, Teams as columns or similar structure
	// Try to detect the format
	lobbies := []Lobby{}

	for i, row := range records[1:] { // Skip header
		if len(row) < 2 {
			continue
		}

		// First column might be lobby name, rest are teams
		lobbyName := strings.TrimSpace(row[0])
		if lobbyName == "" {
			lobbyName = fmt.Sprintf("Lobby %d", i+1)
		}

		teams := []string{}
		for j := 1; j < len(row); j++ {
			if t := strings.TrimSpace(row[j]); t != "" {
				teams = append(teams, t)
			}
		}

		if len(teams) > 0 {
			lobbies = append(lobbies, Lobby{
				ID:    fmt.Sprintf("%s-lobby-%d", strings.ToLower(strings.ReplaceAll(weekName, " ", "-")), i+1),
				Name:  lobbyName,
				Teams: teams,
			})
		}
	}

	weekID := strings.ToLower(strings.ReplaceAll(weekName, " ", "-"))
	week := Week{
		ID:      weekID,
		Number:  weekNum,
		Name:    weekName,
		Lobbies: lobbies,
	}

	if err := saveWeek(week); err != nil {
		return 0, err
	}

	return 1, nil
}

// Alternative: Import from HTML tables when CSV doesn't work
func handleImportFromHTML(w http.ResponseWriter, r *http.Request) {
	session := getSessionFromRequest(r)
	if session == nil || !session.IsAdmin {
		writeError(w, http.StatusForbidden, "Admin access required")
		return
	}

	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON")
		return
	}

	// Fetch the pubhtml page
	resp, err := http.Get(body.URL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to fetch: "+err.Error())
		return
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to parse HTML: "+err.Error())
		return
	}

	// Find all tables and extract data
	tables := extractTables(doc)

	teamsImported := 0
	weeksImported := 0

	for tableName, tableData := range tables {
		nameLower := strings.ToLower(tableName)
		if strings.Contains(nameLower, "team") {
			// Import as teams
			for i, row := range tableData {
				if i == 0 || len(row) < 1 {
					continue // Skip header
				}
				teamName := row[0]
				if teamName == "" {
					continue
				}

				players := []string{}
				for j := 1; j <= 5 && j < len(row); j++ {
					if p := strings.TrimSpace(row[j]); p != "" {
						players = append(players, p)
					}
				}

				subs := []string{}
				for j := 6; j <= 9 && j < len(row); j++ {
					if s := strings.TrimSpace(row[j]); s != "" {
						subs = append(subs, s)
					}
				}

				team := Team{
					ID:      fmt.Sprintf("team-%d", teamsImported+1),
					Name:    teamName,
					Players: players,
					Subs:    subs,
				}
				saveTeam(team)
				teamsImported++
			}
		} else if strings.Contains(nameLower, "week") || strings.Contains(nameLower, "semi") || strings.Contains(nameLower, "final") {
			// Import as week/schedule
			lobbies := []Lobby{}
			for i, row := range tableData {
				if i == 0 || len(row) < 2 {
					continue
				}
				lobbyName := row[0]
				if lobbyName == "" {
					lobbyName = fmt.Sprintf("Lobby %d", i)
				}

				teams := []string{}
				for j := 1; j < len(row); j++ {
					if t := strings.TrimSpace(row[j]); t != "" {
						teams = append(teams, t)
					}
				}

				if len(teams) > 0 {
					lobbies = append(lobbies, Lobby{
						Name:  lobbyName,
						Teams: teams,
					})
				}
			}

			if len(lobbies) > 0 {
				weeksImported++
				weekID := strings.ToLower(strings.ReplaceAll(tableName, " ", "-"))
				week := Week{
					ID:      weekID,
					Number:  weeksImported,
					Name:    tableName,
					Lobbies: lobbies,
				}
				saveWeek(week)
			}
		}
	}

	setSetting("last_sync", time.Now().UTC().Format(time.RFC3339))
	setSetting("published_sheet_url", body.URL)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":       true,
		"teamsImported": teamsImported,
		"weeksImported": weeksImported,
		"tablesFound":   len(tables),
	})
}

func extractTables(doc *html.Node) map[string][][]string {
	tables := make(map[string][][]string)
	tableNum := 0

	var findTables func(*html.Node, string)
	findTables = func(n *html.Node, currentSheet string) {
		// Check for sheet name indicators
		if n.Type == html.ElementNode {
			for _, attr := range n.Attr {
				if attr.Key == "id" && strings.HasPrefix(attr.Val, "sheet-button-") {
					// Found a sheet tab, get its name
					currentSheet = getTextContent(n)
				}
			}
		}

		if n.Type == html.ElementNode && n.Data == "table" {
			tableNum++
			tableName := currentSheet
			if tableName == "" {
				tableName = fmt.Sprintf("Table %d", tableNum)
			}

			var rows [][]string
			var extractRows func(*html.Node)
			extractRows = func(n *html.Node) {
				if n.Type == html.ElementNode && n.Data == "tr" {
					var cells []string
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
							cells = append(cells, strings.TrimSpace(getTextContent(c)))
						}
					}
					if len(cells) > 0 {
						rows = append(rows, cells)
					}
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					extractRows(c)
				}
			}
			extractRows(n)

			if len(rows) > 0 {
				tables[tableName] = rows
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findTables(c, currentSheet)
		}
	}
	findTables(doc, "")

	return tables
}

// Auto-sync from published URL (cron-like)
func syncFromPublishedURL() error {
	url, _ := getSetting("published_sheet_url")
	if url == "" {
		return fmt.Errorf("no published URL configured")
	}

	log.Println("Starting sync from published URL...")

	// Use the HTML import method as it's more reliable
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return err
	}

	tables := extractTables(doc)

	teamsImported := 0
	weeksImported := 0

	for tableName, tableData := range tables {
		nameLower := strings.ToLower(tableName)
		if strings.Contains(nameLower, "team") {
			for i, row := range tableData {
				if i == 0 || len(row) < 1 {
					continue
				}
				teamName := row[0]
				if teamName == "" {
					continue
				}

				players := []string{}
				for j := 1; j <= 5 && j < len(row); j++ {
					if p := strings.TrimSpace(row[j]); p != "" {
						players = append(players, p)
					}
				}

				subs := []string{}
				for j := 6; j <= 9 && j < len(row); j++ {
					if s := strings.TrimSpace(row[j]); s != "" {
						subs = append(subs, s)
					}
				}

				team := Team{
					ID:      fmt.Sprintf("team-%d", teamsImported+1),
					Name:    teamName,
					Players: players,
					Subs:    subs,
				}
				saveTeam(team)
				teamsImported++
			}
		} else if strings.Contains(nameLower, "week") || strings.Contains(nameLower, "semi") || strings.Contains(nameLower, "final") {
			lobbies := []Lobby{}
			for i, row := range tableData {
				if i == 0 || len(row) < 2 {
					continue
				}
				lobbyName := row[0]
				if lobbyName == "" {
					lobbyName = "Lobby " + strconv.Itoa(i)
				}

				teams := []string{}
				for j := 1; j < len(row); j++ {
					if t := strings.TrimSpace(row[j]); t != "" {
						teams = append(teams, t)
					}
				}

				if len(teams) > 0 {
					lobbies = append(lobbies, Lobby{
						Name:  lobbyName,
						Teams: teams,
					})
				}
			}

			if len(lobbies) > 0 {
				weeksImported++
				weekID := strings.ToLower(strings.ReplaceAll(tableName, " ", "-"))
				week := Week{
					ID:      weekID,
					Number:  weeksImported,
					Name:    tableName,
					Lobbies: lobbies,
				}
				saveWeek(week)
			}
		}
	}

	setSetting("last_sync", time.Now().UTC().Format(time.RFC3339))
	log.Printf("Sync complete: %d teams, %d weeks", teamsImported, weeksImported)
	return nil
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

	// Initialize Google Sheets
	if err := initSheetsService(); err != nil {
		log.Printf("Warning: Google Sheets initialization failed: %v", err)
	}

	// Start auto-sync (every 15 minutes)
	startAutoSync(15 * time.Minute)

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

	// Sub pool routes
	r.HandleFunc("/api/subs", handleGetSubs).Methods("GET")
	r.HandleFunc("/api/subs/register", handleRegisterAsSub).Methods("POST")
	r.HandleFunc("/api/subs/availability", handleUpdateSubAvailability).Methods("PUT")
	r.HandleFunc("/api/subs/unregister", handleUnregisterSub).Methods("DELETE")
	r.HandleFunc("/api/admin/import-subs", handleImportSubs).Methods("POST")
	r.HandleFunc("/api/admin/sub-rules", handleGetSubRules).Methods("GET")
	r.HandleFunc("/api/admin/sub-rules", handleSetSubRules).Methods("POST")
	r.HandleFunc("/api/subs/eligible", handleGetEligibleSubs).Methods("GET")
	r.HandleFunc("/api/subs/assignments", handleGetSubAssignments).Methods("GET")
	r.HandleFunc("/api/subs/assign", handleAssignSub).Methods("POST")
	r.HandleFunc("/api/subs/unassign", handleUnassignSub).Methods("POST")

	// Registered players routes
	r.HandleFunc("/api/players", handleGetRegisteredPlayers).Methods("GET")
	r.HandleFunc("/api/admin/import-players", handleImportRegisteredPlayers).Methods("POST")
	r.HandleFunc("/api/players/lookup", handleGetPlayerCR).Methods("GET")

	// Admin routes
	r.HandleFunc("/api/admin/users", handleGetUsers).Methods("GET")
	r.HandleFunc("/api/admin/set-admin", handleSetAdmin).Methods("POST")
	r.HandleFunc("/api/admin/teams", handleCreateTeam).Methods("POST")
	r.HandleFunc("/api/admin/teams/{teamId}", handleUpdateTeam).Methods("PUT")
	r.HandleFunc("/api/admin/teams/{teamId}", handleDeleteTeam).Methods("DELETE")
	r.HandleFunc("/api/admin/import-teams", handleImportTeams).Methods("POST")
	r.HandleFunc("/api/admin/import-weeks", handleImportWeeks).Methods("POST")
	r.HandleFunc("/api/admin/sync", handleSync).Methods("POST")
	r.HandleFunc("/api/admin/sync-status", handleGetSyncStatus).Methods("GET")
	r.HandleFunc("/api/admin/import-from-url", handleImportFromURL).Methods("POST")
	r.HandleFunc("/api/admin/import-from-html", handleImportFromHTML).Methods("POST")

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
