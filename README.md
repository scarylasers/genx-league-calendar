# GenX League Calendar

Team availability and game management system for GenX League with Google Sheets integration.

## Features

- **Google Sheets Sync**: Pull teams, players, and games from your league spreadsheet
- **Multi-Team Support**: Each team has their own dashboard
- **Skill-Based Subs**: Sub requests validate skill rating thresholds (configurable ±20 default)
- **Role Hierarchy**:
  - **League Admin**: Adjust settings, manage all teams, sync data
  - **Team Manager**: Manage roster, set lineups, request subs
  - **Player**: Mark availability, view schedule
- **Discord Integration**: Optional Discord OAuth login and notifications

## Google Sheets Setup

Your spreadsheet should have these sheets (tabs):

### Teams Sheet
| Column | Description |
|--------|-------------|
| team_id | Unique team identifier |
| team_name | Display name |
| manager_discord_id | Discord ID of team manager |

### Players Sheet
| Column | Description |
|--------|-------------|
| player_id | Unique player identifier |
| player_name | Display name |
| team_id | Team they belong to |
| skill_rating | Numeric skill rating (for sub matching) |
| discord_id | Optional Discord ID |

### Games Sheet
| Column | Description |
|--------|-------------|
| game_id | Unique game identifier |
| date | Game date (YYYY-MM-DD) |
| time | Game time (HH:MM) |
| home_team_id | Home team ID |
| away_team_id | Away team ID |
| league | League name |
| division | Division name |

## Environment Variables

```
# Required
GOOGLE_SHEETS_ID=your-spreadsheet-id
GOOGLE_SERVICE_ACCOUNT_JSON={"type":"service_account",...}

# Optional - Discord Integration
DISCORD_CLIENT_ID=xxx
DISCORD_CLIENT_SECRET=xxx
DISCORD_BOT_TOKEN=xxx

# Server
PORT=3000
DATABASE_URL=postgresql://...
SESSION_SECRET=random-32-char-string
BASE_URL=https://your-domain.com
```

## Deployment on Render

1. Fork this repo
2. Create new Web Service on Render
3. Connect your GitHub repo
4. Add environment variables
5. Deploy!

## Local Development

```bash
# Install Go dependencies
go mod tidy

# Set environment variables
export GOOGLE_SHEETS_ID=your-sheet-id
export DATABASE_URL=postgresql://localhost/genx_league

# Run
go run main.go
```

## Settings (League Admin)

- **Skill Threshold**: Max difference allowed between sub and original player (default: 20)
- **Sub Request Window**: Hours before game that subs can be requested
- **Auto-Sync Interval**: How often to sync from Google Sheets

## Customization

This is a boilerplate - customize for your league:

1. Update branding in `index.html` and `styles.css`
2. Modify skill rating logic in `main.go`
3. Add additional Google Sheets columns as needed
4. Adjust role permissions in `handleAuth` functions
