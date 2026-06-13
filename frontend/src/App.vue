<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'

type SeatStatus = 'AVAILABLE' | 'LOCKED' | 'BOOKED'
type Role = 'USER' | 'ADMIN'

interface User {
  id: string
  email: string
  name: string
  picture: string
  role: Role
}

interface Seat {
  id: string
  row: string
  number: number
  status: SeatStatus
  locked_by?: string
  lock_expires_at?: string
}

interface Showtime {
  id: string
  movie: string
  theater: string
  starts_at: string
  seats: Seat[]
}

interface Booking {
  id: string
  user_email: string
  showtime_id: string
  seat_id: string
  status: string
  created_at: string
}

interface AuditLog {
  id: string
  type: string
  message: string
  user_id?: string
  showtime_id?: string
  seat_id?: string
  created_at: string
}

const apiUrl = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'
const wsUrl = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8080'

const storage =
  typeof window !== 'undefined' && typeof window.localStorage?.getItem === 'function'
    ? window.localStorage
    : null

const token = ref(storage?.getItem('ticket_token') ?? '')
const user = ref<User | null>(null)
const showtimes = ref<Showtime[]>([])
const selectedShowtimeId = ref('')
const seats = ref<Seat[]>([])
const bookings = ref<Booking[]>([])
const auditLogs = ref<AuditLog[]>([])
const selectedSeat = ref<Seat | null>(null)
const loading = ref(false)
const message = ref('')
const authError = ref('')
const socketState = ref('offline')
let ws: WebSocket | null = null

const selectedShowtime = computed(() =>
  showtimes.value.find((showtime) => showtime.id === selectedShowtimeId.value),
)

const availableCount = computed(() => seats.value.filter((seat) => seat.status === 'AVAILABLE').length)
const lockedCount = computed(() => seats.value.filter((seat) => seat.status === 'LOCKED').length)
const bookedCount = computed(() => seats.value.filter((seat) => seat.status === 'BOOKED').length)

const myLockedSeat = computed(() =>
  seats.value.find((seat) => seat.status === 'LOCKED' && seat.locked_by === user.value?.id),
)

const seatsByRow = computed(() => {
  const rows = new Map<string, Seat[]>()
  for (const seat of seats.value) {
    if (!rows.has(seat.row)) rows.set(seat.row, [])
    rows.get(seat.row)?.push(seat)
  }
  return [...rows.entries()].map(([row, rowSeats]) => ({
    row,
    seats: rowSeats.sort((a, b) => a.number - b.number),
  }))
})

onMounted(async () => {
  const params = new URLSearchParams(window.location.search)
  const callbackToken = params.get('token')
  const callbackError = params.get('auth_error')

  if (callbackToken) {
    token.value = callbackToken
    storage?.setItem('ticket_token', callbackToken)
    window.history.replaceState({}, '', window.location.pathname)
  }
  if (callbackError) {
    authError.value = `Google login failed: ${callbackError}`
    window.history.replaceState({}, '', window.location.pathname)
  }
  if (token.value) {
    await bootstrap()
  }
})

onUnmounted(() => {
  closeSocket()
})

watch(selectedShowtimeId, async (id) => {
  selectedSeat.value = null
  closeSocket()
  if (id) {
    await loadSeats()
    connectSocket()
  }
})

async function bootstrap() {
  loading.value = true
  try {
    user.value = await request<User>('/api/auth/me')
    await loadShowtimes()
    if (user.value.role === 'ADMIN') {
      await loadAdminData()
    }
  } catch (error) {
    logout()
    authError.value = error instanceof Error ? error.message : 'Authentication failed'
  } finally {
    loading.value = false
  }
}

function loginWithGoogle() {
  window.location.href = `${apiUrl}/api/auth/google/login`
}

function logout() {
  storage?.removeItem('ticket_token')
  token.value = ''
  user.value = null
  showtimes.value = []
  seats.value = []
  selectedShowtimeId.value = ''
  selectedSeat.value = null
  closeSocket()
}

async function loadShowtimes() {
  showtimes.value = await request<Showtime[]>('/api/showtimes')
  selectedShowtimeId.value = showtimes.value[0]?.id ?? ''
}

async function loadSeats() {
  if (!selectedShowtimeId.value) return
  seats.value = await request<Seat[]>(`/api/showtimes/${selectedShowtimeId.value}/seats`)
}

async function lockSeat(seat: Seat) {
  if (seat.status !== 'AVAILABLE') return
  loading.value = true
  message.value = ''
  try {
    await request(`/api/showtimes/${selectedShowtimeId.value}/seats/${seat.id}/lock`, {
      method: 'POST',
    })
    selectedSeat.value = seat
    message.value = `${seat.id} is locked for you. Confirm before the timer expires.`
    await loadSeats()
  } catch (error) {
    message.value = error instanceof Error ? error.message : 'Unable to lock seat'
  } finally {
    loading.value = false
  }
}

async function confirmBooking() {
  const seat = myLockedSeat.value
  if (!seat || !selectedShowtimeId.value) return
  loading.value = true
  message.value = ''
  try {
    await request('/api/bookings/confirm', {
      method: 'POST',
      body: JSON.stringify({ showtime_id: selectedShowtimeId.value, seat_id: seat.id }),
    })
    message.value = `Booking confirmed for ${seat.id}.`
    await loadSeats()
    if (user.value?.role === 'ADMIN') await loadAdminData()
  } catch (error) {
    message.value = error instanceof Error ? error.message : 'Unable to confirm booking'
  } finally {
    loading.value = false
  }
}

async function loadAdminData() {
  bookings.value = await request<Booking[]>('/api/admin/bookings')
  auditLogs.value = await request<AuditLog[]>('/api/admin/audit-logs')
}

function connectSocket() {
  if (!token.value || !selectedShowtimeId.value) return
  ws = new WebSocket(
    `${wsUrl}/ws?showtime_id=${encodeURIComponent(selectedShowtimeId.value)}&token=${encodeURIComponent(token.value)}`,
  )
  socketState.value = 'connecting'
  ws.onopen = () => {
    socketState.value = 'live'
  }
  ws.onclose = () => {
    socketState.value = 'offline'
  }
  ws.onerror = () => {
    socketState.value = 'error'
  }
  ws.onmessage = (event) => {
    const payload = JSON.parse(event.data)
    if (payload.type === 'seats.updated') {
      seats.value = payload.seats
    }
  }
}

function closeSocket() {
  ws?.close()
  ws = null
  socketState.value = 'offline'
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`${apiUrl}${path}`, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token.value ? { Authorization: `Bearer ${token.value}` } : {}),
      ...init.headers,
    },
  })
  const data = await response.json().catch(() => ({}))
  if (!response.ok) {
    throw new Error(data.error ?? `Request failed with ${response.status}`)
  }
  return data
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat('en-US', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(new Date(value))
}

function lockCountdown(seat: Seat) {
  if (!seat.lock_expires_at) return ''
  const seconds = Math.max(0, Math.ceil((new Date(seat.lock_expires_at).getTime() - Date.now()) / 1000))
  const minutes = Math.floor(seconds / 60)
  return `${minutes}:${String(seconds % 60).padStart(2, '0')}`
}
</script>

<template>
  <main class="app-shell">
    <section class="workspace">
      <header class="topbar">
        <button class="brand" type="button" aria-label="Ticket Online home">
          <span class="brand-mark"></span>
          <span>Ticket Online</span>
        </button>

        <div v-if="user" class="user-chip">
          <img v-if="user.picture" :src="user.picture" alt="" />
          <span>{{ user.name || user.email }}</span>
          <strong>{{ user.role }}</strong>
        </div>

        <button v-if="user" class="ghost-button" type="button" @click="logout">Logout</button>
      </header>

      <section v-if="!user" class="login-panel" aria-labelledby="login-title">
        <p class="eyebrow">Cinema Ticket Booking</p>
        <h1 id="login-title">Login to reserve seats in real time</h1>
        <p>
          Google OAuth is required for this demo. Add your Google client values in `.env`, then run
          Docker Compose.
        </p>
        <button class="google-button" type="button" :disabled="loading" @click="loginWithGoogle">
          <span class="google-mark">G</span>
          Login with Google
        </button>
        <p v-if="authError" class="error-text">{{ authError }}</p>
      </section>

      <section v-else class="booking-layout">
        <aside class="showtime-list" aria-label="Showtimes">
          <div class="panel-heading">
            <p class="eyebrow">Showtimes</p>
            <h2>Select a screening</h2>
          </div>
          <button
            v-for="showtime in showtimes"
            :key="showtime.id"
            class="showtime-button"
            :class="{ active: showtime.id === selectedShowtimeId }"
            type="button"
            @click="selectedShowtimeId = showtime.id"
          >
            <strong>{{ showtime.movie }}</strong>
            <span>{{ showtime.theater }} · {{ formatDate(showtime.starts_at) }}</span>
          </button>
        </aside>

        <section class="seat-panel" aria-labelledby="seat-title">
          <div class="panel-heading seat-heading">
            <div>
              <p class="eyebrow">Seat Map</p>
              <h2 id="seat-title">{{ selectedShowtime?.movie }}</h2>
            </div>
            <span class="socket-pill">{{ socketState }}</span>
          </div>

          <div class="screen">SCREEN</div>

          <div class="seat-map">
            <div v-for="row in seatsByRow" :key="row.row" class="seat-row">
              <span class="row-label">{{ row.row }}</span>
              <button
                v-for="seat in row.seats"
                :key="seat.id"
                class="seat"
                :class="[seat.status.toLowerCase(), { mine: seat.locked_by === user?.id }]"
                type="button"
                :disabled="seat.status !== 'AVAILABLE' || loading"
                :title="seat.status"
                @click="lockSeat(seat)"
              >
                {{ seat.number }}
              </button>
            </div>
          </div>

          <div class="stats-bar">
            <span><b>{{ availableCount }}</b> available</span>
            <span><b>{{ lockedCount }}</b> locked</span>
            <span><b>{{ bookedCount }}</b> booked</span>
          </div>

          <div class="checkout-strip">
            <div>
              <strong>{{ myLockedSeat ? `Seat ${myLockedSeat.id}` : 'No active seat lock' }}</strong>
              <span v-if="myLockedSeat">Expires in {{ lockCountdown(myLockedSeat) }}</span>
              <span v-else>Choose an available seat to start the 5-minute lock.</span>
            </div>
            <button class="primary-button" type="button" :disabled="!myLockedSeat || loading" @click="confirmBooking">
              Confirm Booking
            </button>
          </div>

          <p v-if="message" class="status-message">{{ message }}</p>
        </section>

        <section v-if="user.role === 'ADMIN'" class="admin-panel" aria-label="Admin dashboard">
          <div class="panel-heading">
            <p class="eyebrow">Admin</p>
            <h2>Bookings & audit logs</h2>
          </div>
          <button class="ghost-button" type="button" @click="loadAdminData">Refresh</button>

          <div class="admin-columns">
            <div>
              <h3>Bookings</h3>
              <ul>
                <li v-for="booking in bookings" :key="booking.id">
                  <b>{{ booking.seat_id }}</b>
                  <span>{{ booking.user_email }}</span>
                </li>
              </ul>
            </div>
            <div>
              <h3>Audit Logs</h3>
              <ul>
                <li v-for="log in auditLogs" :key="log.id">
                  <b>{{ log.type }}</b>
                  <span>{{ log.seat_id || '-' }} · {{ log.message }}</span>
                </li>
              </ul>
            </div>
          </div>
        </section>
      </section>
    </section>
  </main>
</template>
