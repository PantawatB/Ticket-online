package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	roleUser        = "USER"
	roleAdmin       = "ADMIN"
	statusAvailable = "AVAILABLE"
	statusLocked    = "LOCKED"
	statusBooked    = "BOOKED"
	eventChannel    = "booking-events"
)

type Config struct {
	Port              string
	FrontendURL       string
	MongoURI          string
	MongoDB           string
	RedisAddr         string
	GoogleClientID    string
	GoogleClientSecret string
	GoogleRedirectURL string
	JWTSecret         string
	AdminEmails       map[string]bool
	LockTTL           time.Duration
}

type App struct {
	cfg       Config
	db        *mongo.Database
	redis     *redis.Client
	hub       *Hub
	http      *http.ServeMux
	startedAt time.Time
}

type User struct {
	ID        string    `bson:"_id" json:"id"`
	Email     string    `bson:"email" json:"email"`
	Name      string    `bson:"name" json:"name"`
	Picture   string    `bson:"picture" json:"picture"`
	Role      string    `bson:"role" json:"role"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

type Showtime struct {
	ID        string    `bson:"_id" json:"id"`
	Movie     string    `bson:"movie" json:"movie"`
	Theater   string    `bson:"theater" json:"theater"`
	StartsAt  time.Time `bson:"starts_at" json:"starts_at"`
	Seats     []Seat    `bson:"seats" json:"seats"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
}

type Seat struct {
	ID            string     `bson:"id" json:"id"`
	Row           string     `bson:"row" json:"row"`
	Number        int        `bson:"number" json:"number"`
	Status        string     `bson:"status" json:"status"`
	LockedBy      string     `bson:"locked_by,omitempty" json:"locked_by,omitempty"`
	LockExpiresAt *time.Time `bson:"lock_expires_at,omitempty" json:"lock_expires_at,omitempty"`
}

type Booking struct {
	ID         string    `bson:"_id" json:"id"`
	UserID     string    `bson:"user_id" json:"user_id"`
	UserName   string    `bson:"user_name" json:"user_name"`
	UserEmail  string    `bson:"user_email" json:"user_email"`
	ShowtimeID string    `bson:"showtime_id" json:"showtime_id"`
	EventTitle string    `bson:"event_title,omitempty" json:"event_title,omitempty"`
	SeatID     string    `bson:"seat_id" json:"seat_id"`
	Status     string    `bson:"status" json:"status"`
	CreatedAt  time.Time `bson:"created_at" json:"created_at"`
}

type AuditLog struct {
	ID         string    `bson:"_id" json:"id"`
	Type       string    `bson:"type" json:"type"`
	Message    string    `bson:"message" json:"message"`
	UserID     string    `bson:"user_id,omitempty" json:"user_id,omitempty"`
	ShowtimeID string    `bson:"showtime_id,omitempty" json:"showtime_id,omitempty"`
	SeatID     string    `bson:"seat_id,omitempty" json:"seat_id,omitempty"`
	CreatedAt  time.Time `bson:"created_at" json:"created_at"`
}

type BookingEvent struct {
	Type       string `json:"type"`
	Message    string `json:"message"`
	UserID     string `json:"user_id,omitempty"`
	ShowtimeID string `json:"showtime_id,omitempty"`
	SeatID     string `json:"seat_id,omitempty"`
}

type GoogleProfile struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	EmailVerified bool   `json:"email_verified"`
}

type contextKey string

const userKey contextKey = "user"

func main() {
	ctx := context.Background()
	cfg := loadConfig()

	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(cfg.MongoURI))
	if err != nil {
		log.Fatal(err)
	}
	if err := mongoClient.Ping(ctx, nil); err != nil {
		log.Fatal(err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal(err)
	}

	app := &App{
		cfg:       cfg,
		db:        mongoClient.Database(cfg.MongoDB),
		redis:     rdb,
		hub:       NewHub(),
		http:      http.NewServeMux(),
		startedAt: time.Now(),
	}
	if err := app.seed(ctx); err != nil {
		log.Fatal(err)
	}
	app.routes()
	go app.consumeEvents(ctx)
	go app.releaseExpiredLocks(ctx)

	addr := ":" + cfg.Port
	log.Printf("backend listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, app.cors(app.http)))
}

func loadConfig() Config {
	ttl, err := time.ParseDuration(env("LOCK_TTL", "5m"))
	if err != nil {
		ttl = 5 * time.Minute
	}
	admins := map[string]bool{}
	for _, email := range strings.Split(env("ADMIN_EMAILS", ""), ",") {
		email = strings.ToLower(strings.TrimSpace(email))
		if email != "" {
			admins[email] = true
		}
	}
	return Config{
		Port:               env("PORT", "8080"),
		FrontendURL:        env("FRONTEND_URL", "http://localhost:5173"),
		MongoURI:           env("MONGO_URI", "mongodb://mongodb:27017"),
		MongoDB:            env("MONGO_DB", "ticket_online"),
		RedisAddr:          env("REDIS_ADDR", "redis:6379"),
		GoogleClientID:     env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: env("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:  env("GOOGLE_REDIRECT_URL", "http://localhost:8080/api/auth/google/callback"),
		JWTSecret:          env("JWT_SECRET", "change-me-in-env"),
		AdminEmails:        admins,
		LockTTL:            ttl,
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (a *App) routes() {
	a.http.HandleFunc("/health", a.handleHealth)
	a.http.HandleFunc("/api/auth/google/login", a.handleGoogleLogin)
	a.http.HandleFunc("/api/auth/google/callback", a.handleGoogleCallback)
	a.http.Handle("/api/auth/me", a.auth(http.HandlerFunc(a.handleMe), roleUser, roleAdmin))
	a.http.Handle("/api/showtimes", a.auth(http.HandlerFunc(a.handleShowtimes), roleUser, roleAdmin))
	a.http.Handle("/api/showtimes/", a.auth(http.HandlerFunc(a.handleShowtimeAction), roleUser, roleAdmin))
	a.http.Handle("/api/bookings/confirm", a.auth(http.HandlerFunc(a.handleConfirmBooking), roleUser, roleAdmin))
	a.http.Handle("/api/admin/bookings", a.auth(http.HandlerFunc(a.handleAdminBookings), roleAdmin))
	a.http.Handle("/api/admin/audit-logs", a.auth(http.HandlerFunc(a.handleAdminAuditLogs), roleAdmin))
	a.http.Handle("/ws", a.auth(http.HandlerFunc(a.handleWebSocket), roleUser, roleAdmin))
}

func (a *App) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", a.cfg.FrontendURL)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "started_at": a.startedAt})
}

func (a *App) handleGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if a.cfg.GoogleClientID == "" || a.cfg.GoogleClientSecret == "" {
		writeError(w, http.StatusServiceUnavailable, "Google OAuth is not configured. Set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET in .env.")
		return
	}
	state := randomID()
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: state, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	authURL := "https://accounts.google.com/o/oauth2/v2/auth?" + url.Values{
		"client_id":     {a.cfg.GoogleClientID},
		"redirect_uri":  {a.cfg.GoogleRedirectURL},
		"response_type": {"code"},
		"scope":         {"openid email profile"},
		"state":         {state},
		"access_type":   {"offline"},
		"prompt":        {"select_account"},
	}.Encode()
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (a *App) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie("oauth_state")
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		a.redirectAuthError(w, r, "invalid_oauth_state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		a.redirectAuthError(w, r, "missing_code")
		return
	}
	accessToken, err := a.exchangeGoogleCode(r.Context(), code)
	if err != nil {
		a.publishEvent(r.Context(), BookingEvent{Type: "System Error", Message: "Google token exchange failed: " + err.Error()})
		a.redirectAuthError(w, r, "token_exchange_failed")
		return
	}
	profile, err := fetchGoogleProfile(r.Context(), accessToken)
	if err != nil {
		a.publishEvent(r.Context(), BookingEvent{Type: "System Error", Message: "Google profile fetch failed: " + err.Error()})
		a.redirectAuthError(w, r, "profile_fetch_failed")
		return
	}
	user, err := a.upsertUser(r.Context(), profile)
	if err != nil {
		a.redirectAuthError(w, r, "user_upsert_failed")
		return
	}
	token, err := signJWT(user, a.cfg.JWTSecret)
	if err != nil {
		a.redirectAuthError(w, r, "jwt_failed")
		return
	}
	redirectURL, _ := url.Parse(a.cfg.FrontendURL)
	http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: "ticket_token", Value: token, Path: "/", SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

func (a *App) redirectAuthError(w http.ResponseWriter, r *http.Request, message string) {
	redirectURL, _ := url.Parse(a.cfg.FrontendURL)
	query := redirectURL.Query()
	query.Set("auth_error", message)
	redirectURL.RawQuery = query.Encode()
	http.Redirect(w, r, redirectURL.String(), http.StatusFound)
}

func (a *App) exchangeGoogleCode(ctx context.Context, code string) (string, error) {
	body := url.Values{
		"client_id":     {a.cfg.GoogleClientID},
		"client_secret": {a.cfg.GoogleClientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {a.cfg.GoogleRedirectURL},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(body.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return "", fmt.Errorf("google token endpoint returned %s: %s", res.Status, string(data))
	}
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	if parsed.AccessToken == "" {
		return "", errors.New("empty access token")
	}
	return parsed.AccessToken, nil
}

func fetchGoogleProfile(ctx context.Context, token string) (*GoogleProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v3/userinfo", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("google userinfo returned %s: %s", res.Status, string(data))
	}
	var profile GoogleProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, err
	}
	if profile.Sub == "" || profile.Email == "" {
		return nil, errors.New("google profile missing sub or email")
	}
	if !profile.EmailVerified {
		return nil, errors.New("google profile email is not verified")
	}
	return &profile, nil
}

func (a *App) upsertUser(ctx context.Context, profile *GoogleProfile) (*User, error) {
	now := time.Now().UTC()
	role := roleUser
	email := strings.ToLower(profile.Email)
	if a.cfg.AdminEmails[email] {
		role = roleAdmin
	}
	user := User{
		ID:        "google:" + profile.Sub,
		Email:     email,
		Name:      profile.Name,
		Picture:   profile.Picture,
		Role:      role,
		UpdatedAt: now,
	}
	update := bson.M{
		"$set": bson.M{"email": user.Email, "name": user.Name, "picture": user.Picture, "role": user.Role, "updated_at": now},
		"$setOnInsert": bson.M{"created_at": now},
	}
	_, err := a.db.Collection("users").UpdateByID(ctx, user.ID, update, options.Update().SetUpsert(true))
	if err != nil {
		return nil, err
	}
	err = a.db.Collection("users").FindOne(ctx, bson.M{"_id": user.ID}).Decode(&user)
	return &user, err
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, currentUser(r))
}

func (a *App) handleShowtimes(w http.ResponseWriter, r *http.Request) {
	cursor, err := a.db.Collection("showtimes").Find(r.Context(), bson.M{}, options.Find().SetSort(bson.M{"starts_at": 1}))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var showtimes []Showtime
	if err := cursor.All(r.Context(), &showtimes); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, showtimes)
}

func (a *App) handleShowtimeAction(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/showtimes/"), "/")
	if len(parts) == 2 && parts[1] == "seats" && r.Method == http.MethodGet {
		a.handleSeats(w, r, parts[0])
		return
	}
	if len(parts) == 4 && parts[1] == "seats" && parts[3] == "lock" && r.Method == http.MethodPost {
		a.handleLockSeat(w, r, parts[0], parts[2])
		return
	}
	if len(parts) == 4 && parts[1] == "seats" && parts[3] == "release" && r.Method == http.MethodPost {
		a.handleReleaseSeat(w, r, parts[0], parts[2])
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (a *App) handleSeats(w http.ResponseWriter, r *http.Request, showtimeID string) {
	var showtime Showtime
	if err := a.db.Collection("showtimes").FindOne(r.Context(), bson.M{"_id": showtimeID}).Decode(&showtime); err != nil {
		writeError(w, http.StatusNotFound, "showtime not found")
		return
	}
	writeJSON(w, http.StatusOK, showtime.Seats)
}

func (a *App) handleLockSeat(w http.ResponseWriter, r *http.Request, showtimeID, seatID string) {
	user := currentUser(r)
	lockKey := seatLockKey(showtimeID, seatID)
	ok, err := a.redis.SetNX(r.Context(), lockKey, user.ID, a.cfg.LockTTL).Result()
	if err != nil {
		a.publishEvent(r.Context(), BookingEvent{Type: "Lock Fail", Message: err.Error(), UserID: user.ID, ShowtimeID: showtimeID, SeatID: seatID})
		writeError(w, http.StatusConflict, "seat lock failed")
		return
	}
	if !ok {
		a.publishEvent(r.Context(), BookingEvent{Type: "Lock Fail", Message: "seat already locked", UserID: user.ID, ShowtimeID: showtimeID, SeatID: seatID})
		writeError(w, http.StatusConflict, "seat is already locked")
		return
	}

	expiresAt := time.Now().UTC().Add(a.cfg.LockTTL)
	res, err := a.db.Collection("showtimes").UpdateOne(
		r.Context(),
		bson.M{"_id": showtimeID, "seats": bson.M{"$elemMatch": bson.M{"id": seatID, "status": statusAvailable}}},
		bson.M{"$set": bson.M{"seats.$.status": statusLocked, "seats.$.locked_by": user.ID, "seats.$.lock_expires_at": expiresAt}},
	)
	if err != nil || res.ModifiedCount != 1 {
		a.redis.Del(r.Context(), lockKey)
		a.publishEvent(r.Context(), BookingEvent{Type: "Lock Fail", Message: "seat was not available", UserID: user.ID, ShowtimeID: showtimeID, SeatID: seatID})
		writeError(w, http.StatusConflict, "seat is not available")
		return
	}
	a.broadcastSeats(r.Context(), showtimeID)
	writeJSON(w, http.StatusOK, map[string]any{"seat_id": seatID, "status": statusLocked, "expires_at": expiresAt})
}

func (a *App) handleReleaseSeat(w http.ResponseWriter, r *http.Request, showtimeID, seatID string) {
	user := currentUser(r)
	res, err := a.db.Collection("showtimes").UpdateOne(
		r.Context(),
		bson.M{"_id": showtimeID, "seats": bson.M{"$elemMatch": bson.M{"id": seatID, "status": statusLocked, "locked_by": user.ID}}},
		bson.M{"$set": bson.M{"seats.$.status": statusAvailable}, "$unset": bson.M{"seats.$.locked_by": "", "seats.$.lock_expires_at": ""}},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if res.ModifiedCount != 1 {
		writeError(w, http.StatusConflict, "seat is not locked by this user")
		return
	}
	_ = a.redis.Del(r.Context(), seatLockKey(showtimeID, seatID)).Err()
	a.publishEvent(r.Context(), BookingEvent{Type: "Seat Released", Message: "seat released by user", UserID: user.ID, ShowtimeID: showtimeID, SeatID: seatID})
	a.broadcastSeats(r.Context(), showtimeID)
	writeJSON(w, http.StatusOK, map[string]any{"seat_id": seatID, "status": statusAvailable})
}

func (a *App) handleConfirmBooking(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var input struct {
		ShowtimeID string `json:"showtime_id"`
		EventTitle string `json:"event_title"`
		SeatID     string `json:"seat_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.ShowtimeID == "" || input.SeatID == "" {
		writeError(w, http.StatusBadRequest, "showtime_id and seat_id are required")
		return
	}
	res, err := a.db.Collection("showtimes").UpdateOne(
		r.Context(),
		bson.M{"_id": input.ShowtimeID, "seats": bson.M{"$elemMatch": bson.M{"id": input.SeatID, "status": statusLocked, "locked_by": user.ID, "lock_expires_at": bson.M{"$gt": time.Now().UTC()}}}},
		bson.M{"$set": bson.M{"seats.$.status": statusBooked}, "$unset": bson.M{"seats.$.locked_by": "", "seats.$.lock_expires_at": ""}},
	)
	if err != nil || res.ModifiedCount != 1 {
		writeError(w, http.StatusConflict, "seat is not locked by this user or lock expired")
		return
	}
	booking := Booking{
		ID:         randomID(),
		UserID:     user.ID,
		UserName:   user.Name,
		UserEmail:  user.Email,
		ShowtimeID: input.ShowtimeID,
		EventTitle: input.EventTitle,
		SeatID:     input.SeatID,
		Status:     "CONFIRMED",
		CreatedAt:  time.Now().UTC(),
	}
	if _, err := a.db.Collection("bookings").InsertOne(r.Context(), booking); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.redis.Del(r.Context(), seatLockKey(input.ShowtimeID, input.SeatID))
	a.publishEvent(r.Context(), BookingEvent{Type: "Booking Success", Message: "booking confirmed", UserID: user.ID, ShowtimeID: input.ShowtimeID, SeatID: input.SeatID})
	a.broadcastSeats(r.Context(), input.ShowtimeID)
	a.hub.Broadcast(input.ShowtimeID, map[string]any{
		"type":        "booking.notification",
		"message":     "Booking confirmed successfully",
		"event_title": input.EventTitle,
		"seat_id":     input.SeatID,
		"user_name":   user.Name,
	})
	writeJSON(w, http.StatusOK, booking)
}

func (a *App) handleAdminBookings(w http.ResponseWriter, r *http.Request) {
	filter := bson.M{}
	if v := r.URL.Query().Get("showtime_id"); v != "" {
		filter["showtime_id"] = v
	}
	if v := r.URL.Query().Get("user_id"); v != "" {
		filter["user_id"] = v
	}
	if v := r.URL.Query().Get("status"); v != "" {
		filter["status"] = v
	}
	cursor, err := a.db.Collection("bookings").Find(r.Context(), filter, options.Find().SetSort(bson.M{"created_at": -1}))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var bookings []Booking
	if err := cursor.All(r.Context(), &bookings); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bookings)
}

func (a *App) handleAdminAuditLogs(w http.ResponseWriter, r *http.Request) {
	cursor, err := a.db.Collection("audit_logs").Find(r.Context(), bson.M{}, options.Find().SetSort(bson.M{"created_at": -1}).SetLimit(100))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var logs []AuditLog
	if err := cursor.All(r.Context(), &logs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

func (a *App) auth(next http.Handler, roles ...string) http.Handler {
	allowed := map[string]bool{}
	for _, role := range roles {
		allowed[role] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			if r.URL.Path == "/ws" {
				token = r.URL.Query().Get("token")
			}
		}
		user, err := parseJWT(token, a.cfg.JWTSecret)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !allowed[user.Role] {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	})
}

func bearerToken(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if strings.HasPrefix(value, "Bearer ") {
		return strings.TrimPrefix(value, "Bearer ")
	}
	return ""
}

func currentUser(r *http.Request) *User {
	user, _ := r.Context().Value(userKey).(*User)
	return user
}

func signJWT(user *User, secret string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString(mustJSON(map[string]string{"alg": "HS256", "typ": "JWT"}))
	payload := base64.RawURLEncoding.EncodeToString(mustJSON(map[string]any{
		"sub": user.ID, "email": user.Email, "name": user.Name, "picture": user.Picture, "role": user.Role, "exp": time.Now().Add(24 * time.Hour).Unix(),
	}))
	unsigned := header + "." + payload
	return unsigned + "." + sign(unsigned, secret), nil
}

func parseJWT(token, secret string) (*User, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || !hmac.Equal([]byte(parts[2]), []byte(sign(parts[0]+"."+parts[1], secret))) {
		return nil, errors.New("invalid token")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var payload struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
		Role    string `json:"role"`
		Exp     int64  `json:"exp"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	if payload.Exp < time.Now().Unix() {
		return nil, errors.New("expired token")
	}
	return &User{ID: payload.Sub, Email: payload.Email, Name: payload.Name, Picture: payload.Picture, Role: payload.Role}, nil
}

func sign(data, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(data))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func randomID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func seatLockKey(showtimeID, seatID string) string {
	return "seat-lock:" + showtimeID + ":" + seatID
}

func (a *App) seed(ctx context.Context) error {
	return a.ensureSeedShowtimes(ctx)
}

func makeSeats() []Seat {
	var seats []Seat
	for _, row := range []string{"A", "B", "C", "D", "E", "F", "G", "H"} {
		for number := 1; number <= 12; number++ {
			seats = append(seats, Seat{ID: fmt.Sprintf("%s%d", row, number), Row: row, Number: number, Status: statusAvailable})
		}
	}
	return seats
}

func (a *App) ensureSeatCapacity(ctx context.Context) error {
	cursor, err := a.db.Collection("showtimes").Find(ctx, bson.M{})
	if err != nil {
		return err
	}
	var showtimes []Showtime
	if err := cursor.All(ctx, &showtimes); err != nil {
		return err
	}
	desiredSeats := makeSeats()
	for _, showtime := range showtimes {
		existing := map[string]bool{}
		for _, seat := range showtime.Seats {
			existing[seat.ID] = true
		}
		var missing []any
		for _, seat := range desiredSeats {
			if !existing[seat.ID] {
				missing = append(missing, seat)
			}
		}
		if len(missing) == 0 {
			continue
		}
		_, err := a.db.Collection("showtimes").UpdateOne(
			ctx,
			bson.M{"_id": showtime.ID},
			bson.M{"$push": bson.M{"seats": bson.M{"$each": missing}}},
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *App) ensureSeedShowtimes(ctx context.Context) error {
	now := time.Now().UTC()
	seedShowtimes := []Showtime{
		{ID: "show-001", Movie: "Official After Party Awakenings Festival 2018", Theater: "Cinema 1", StartsAt: now.Add(2 * time.Hour), Seats: makeSeats(), CreatedAt: now},
		{ID: "show-002", Movie: "Tomorrowland 2019 - Weekend 1 Full Madness Pass", Theater: "Cinema 2", StartsAt: now.Add(3 * time.Hour), Seats: makeSeats(), CreatedAt: now},
		{ID: "show-003", Movie: "Dekmantel Festival 2019 - Wednesday", Theater: "Cinema 3", StartsAt: now.Add(4 * time.Hour), Seats: makeSeats(), CreatedAt: now},
		{ID: "show-004", Movie: "Katy Perry & Santana - New Orleans Jazz and Heritage", Theater: "Cinema 4", StartsAt: now.Add(5 * time.Hour), Seats: makeSeats(), CreatedAt: now},
	}

	for _, showtime := range seedShowtimes {
		update := bson.M{
			"$set": bson.M{
				"movie":     showtime.Movie,
				"theater":   showtime.Theater,
				"starts_at": showtime.StartsAt,
			},
			"$setOnInsert": bson.M{
				"_id":        showtime.ID,
				"seats":      showtime.Seats,
				"created_at": showtime.CreatedAt,
			},
		}
		_, err := a.db.Collection("showtimes").UpdateOne(
			ctx,
			bson.M{"_id": showtime.ID},
			update,
			options.Update().SetUpsert(true),
		)
		if err != nil {
			return err
		}
	}

	return a.ensureSeatCapacity(ctx)
}

func (a *App) publishEvent(ctx context.Context, event BookingEvent) {
	data, _ := json.Marshal(event)
	_ = a.redis.Publish(ctx, eventChannel, data).Err()
}

func (a *App) consumeEvents(ctx context.Context) {
	pubsub := a.redis.Subscribe(ctx, eventChannel)
	defer pubsub.Close()
	for msg := range pubsub.Channel() {
		var event BookingEvent
		if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
			continue
		}
		log.Printf("mock notification: %s %s", event.Type, event.Message)
		audit := AuditLog{ID: randomID(), Type: event.Type, Message: event.Message, UserID: event.UserID, ShowtimeID: event.ShowtimeID, SeatID: event.SeatID, CreatedAt: time.Now().UTC()}
		_, _ = a.db.Collection("audit_logs").InsertOne(ctx, audit)
	}
}

func (a *App) releaseExpiredLocks(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		cursor, err := a.db.Collection("showtimes").Find(ctx, bson.M{"seats": bson.M{"$elemMatch": bson.M{"status": statusLocked, "lock_expires_at": bson.M{"$lte": time.Now().UTC()}}}})
		if err != nil {
			continue
		}
		var showtimes []Showtime
		if err := cursor.All(ctx, &showtimes); err != nil {
			continue
		}
		for _, showtime := range showtimes {
			changed := false
			for _, seat := range showtime.Seats {
				if seat.Status == statusLocked && seat.LockExpiresAt != nil && seat.LockExpiresAt.Before(time.Now().UTC()) {
					_, _ = a.db.Collection("showtimes").UpdateOne(ctx, bson.M{"_id": showtime.ID, "seats.id": seat.ID}, bson.M{"$set": bson.M{"seats.$.status": statusAvailable}, "$unset": bson.M{"seats.$.locked_by": "", "seats.$.lock_expires_at": ""}})
					_ = a.redis.Del(ctx, seatLockKey(showtime.ID, seat.ID)).Err()
					a.publishEvent(ctx, BookingEvent{Type: "Booking Timeout", Message: "seat lock expired", UserID: seat.LockedBy, ShowtimeID: showtime.ID, SeatID: seat.ID})
					a.publishEvent(ctx, BookingEvent{Type: "Seat Released", Message: "seat returned to available", UserID: seat.LockedBy, ShowtimeID: showtime.ID, SeatID: seat.ID})
					changed = true
				}
			}
			if changed {
				a.broadcastSeats(ctx, showtime.ID)
			}
		}
	}
}

func (a *App) broadcastSeats(ctx context.Context, showtimeID string) {
	var showtime Showtime
	if err := a.db.Collection("showtimes").FindOne(ctx, bson.M{"_id": showtimeID}).Decode(&showtime); err != nil {
		return
	}
	a.hub.Broadcast(showtimeID, map[string]any{"type": "seats.updated", "showtime_id": showtimeID, "seats": showtime.Seats})
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string]map[*websocket.Conn]bool
}

func NewHub() *Hub {
	return &Hub{clients: map[string]map[*websocket.Conn]bool{}}
}

func (h *Hub) Add(showtimeID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.clients[showtimeID] == nil {
		h.clients[showtimeID] = map[*websocket.Conn]bool{}
	}
	h.clients[showtimeID][conn] = true
}

func (h *Hub) Remove(showtimeID string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients[showtimeID], conn)
}

func (h *Hub) Broadcast(showtimeID string, payload any) {
	data, _ := json.Marshal(payload)
	h.mu.RLock()
	conns := make([]*websocket.Conn, 0, len(h.clients[showtimeID]))
	for conn := range h.clients[showtimeID] {
		conns = append(conns, conn)
	}
	h.mu.RUnlock()
	for _, conn := range conns {
		_ = conn.WriteMessage(websocket.TextMessage, data)
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (a *App) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	showtimeID := r.URL.Query().Get("showtime_id")
	if showtimeID == "" {
		writeError(w, http.StatusBadRequest, "showtime_id is required")
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	a.hub.Add(showtimeID, conn)
	a.broadcastSeats(r.Context(), showtimeID)
	defer func() {
		a.hub.Remove(showtimeID, conn)
		conn.Close()
	}()
	for {
		if _, _, err := conn.NextReader(); err != nil {
			return
		}
	}
}
