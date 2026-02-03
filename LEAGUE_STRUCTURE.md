# GenX League Structure

This document describes the league structure for importing data into the calendar system.
Update this if the format changes between seasons.

---

## Season 6 Structure (Reference)

### Teams (20 total)

Each team has:
- **5 Main Players** (Player 1-5)
- **4 Substitute Players** (Sub 1-4)

| # | Team Name |
|---|-----------|
| 1 | BabyRuthless Deez Nuts |
| 2 | Baggers and Taggers |
| 3 | Blow Pops |
| 4 | Booty Pebbles |
| 5 | Captain Crunch Berries |
| 6 | Dunk-My Balls |
| 7 | Everlasting Squad Stoppers |
| 8 | Fruit Loops |
| 9 | Fruit Stripe Strippers |
| 10 | Honey Bunches Of Nuts |
| 11 | Hot Pocket Pounders |
| 12 | PopRocks SkullFukers |
| 13 | Porn Flakes |
| 14 | Sour Patch Blasters |
| 15 | Stay Puft Poppers |
| 16 | The Natty Lites |
| 17 | Tootsie and the Rollers |
| 18 | Uh Oh SpaghettiOs |
| 19 | WarHeads |
| 20 | Yippee-Ki-Yay |

---

### Schedule Format

**Regular Season:** 6 weeks
**Playoffs:** SemiFinals + Finals

Each week has **3 lobbies** with **~7 teams per lobby**.

#### Lobby Format
- Teams are assigned to lobbies each week
- All teams in a lobby play together in the same session
- Multiple rounds/games per session

#### Schedule Sheet Columns
```
Week | Lobby | Team 1 | Team 2 | Team 3 | Team 4 | Team 5 | Team 6 | Team 7
```

#### Example Week
```
Week 1:
  - Lobby 1: Team A, Team B, Team C, Team D, Team E, Team F, Team G
  - Lobby 2: Team H, Team I, Team J, Team K, Team L, Team M, Team N
  - Lobby 3: Team O, Team P, Team Q, Team R, Team S, Team T
```

---

## Data Import Format

### Teams JSON Format

```json
[
  {
    "name": "Team Name",
    "players": ["Player1", "Player2", "Player3", "Player4", "Player5"],
    "subs": ["Sub1", "Sub2", "Sub3", "Sub4"]
  }
]
```

### Weeks/Schedule JSON Format

```json
[
  {
    "id": "week-1",
    "number": 1,
    "name": "Week 1",
    "date": "2025-03-15",
    "time": "20:00",
    "lobbies": [
      {
        "name": "Lobby 1",
        "teams": ["Team A", "Team B", "Team C", "Team D", "Team E", "Team F", "Team G"]
      },
      {
        "name": "Lobby 2",
        "teams": ["Team H", "Team I", "Team J", "Team K", "Team L", "Team M", "Team N"]
      },
      {
        "name": "Lobby 3",
        "teams": ["Team O", "Team P", "Team Q", "Team R", "Team S", "Team T"]
      }
    ]
  },
  {
    "id": "semifinals",
    "number": 7,
    "name": "SemiFinals",
    "date": "2025-04-26",
    "time": "20:00",
    "lobbies": [
      {
        "name": "Upper Bracket",
        "teams": ["Top 10 teams..."]
      },
      {
        "name": "Lower Bracket",
        "teams": ["Bottom 10 teams..."]
      }
    ]
  }
]
```

---

## Spreadsheet Tabs Reference

| Tab Name | Purpose |
|----------|---------|
| **Teams** | Team rosters (Name, Player 1-5, Sub 1-4) |
| **Schedule** | Weekly lobby assignments |
| **Week 1-6** | Detailed scoring per week |
| **SemiFinals** | Playoff bracket |
| **Finals** | Championship results |
| **PlayerResults** | Individual player stats |
| **TeamResults** | Aggregated team stats |
| **Sessions** | Sessions played per team |
| **Score Leaders** | Leaderboards |

---

## Availability Tracking

For each week, players can mark:
- **Available** - Can play this week
- **Unavailable** - Cannot play this week

The system calculates:
- How many main roster players are unavailable
- How many subs are needed

---

## Roles

| Role | Permissions |
|------|-------------|
| **Player** | Mark own availability, view schedule |
| **Team Captain** | View team availability (future: manage roster) |
| **League Admin** | Import data, announce weeks, manage settings |

---

## Notes for Season 7

Update this section with any changes:

- [ ] Number of teams changed?
- [ ] Roster size changed (5+4)?
- [ ] Lobby format changed?
- [ ] Schedule format changed?
- [ ] New playoff structure?

---

## Converting Spreadsheet to JSON

### Quick Python Script

```python
import pandas as pd
import json

# Read Teams sheet
df = pd.read_excel('GenX - S7 Scoring Sheet.xlsx', sheet_name='Teams')

teams = []
for _, row in df.iterrows():
    if pd.notna(row['Team Name']):
        team = {
            'name': row['Team Name'],
            'players': [row[f'Player {i}'] for i in range(1, 6) if pd.notna(row.get(f'Player {i}'))],
            'subs': [row[f'Sub {i}'] for i in range(1, 5) if pd.notna(row.get(f'Sub {i}'))]
        }
        teams.append(team)

print(json.dumps(teams, indent=2))
```

### Manual Process

1. Open spreadsheet in Google Sheets/Excel
2. Copy Teams tab data
3. Format as JSON array
4. Paste into Import Teams modal

---

## Contact

For questions about the league structure, contact the GenX League admins.
