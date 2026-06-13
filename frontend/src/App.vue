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
  user_name?: string
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

interface MarketingEvent {
  id: number
  title: string
  date: string
  venue: string
  category: string
  price: string
  description: string
  showtimeId: string
  posterClass: string
}

const apiUrl = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'
const wsUrl = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8080'

const storage =
  typeof window !== 'undefined' && typeof window.localStorage?.getItem === 'function'
    ? window.localStorage
    : null

const token = ref(storage?.getItem('ticket_token') ?? readCookie('ticket_token') ?? '')
const user = ref<User | null>(null)
const showtimes = ref<Showtime[]>([])
const selectedShowtimeId = ref('')
const seats = ref<Seat[]>([])
const bookings = ref<Booking[]>([])
const auditLogs = ref<AuditLog[]>([])
const selectedSeatIds = ref<string[]>([])
const selectedEvent = ref<MarketingEvent | null>(null)
const searchQuery = ref('')
const selectedCategory = ref('All Category')
const isBookingView = ref(false)
const loading = ref(false)
const message = ref('')
const authError = ref('')
const socketState = ref('offline')
const nowTick = ref(Date.now())
const paymentDialogOpen = ref(false)
let ws: WebSocket | null = null
let timer: number | undefined

const marketingEvents: MarketingEvent[] = [
  {
    id: 1,
    title: 'Official After Party Awakenings Festival 2018',
    date: '10 December, 2019',
    venue: 'Bangkok Hall',
    category: 'Festival',
    price: '€ 73.54',
    description: 'A late-night electronic set with reserved cinema-style seats for the demo booking flow.',
    showtimeId: 'show-001',
    posterClass: 'poster-bien',
  },
  {
    id: 2,
    title: 'Dekmantel Festival 2019 - Wednesday',
    date: '12 September, 2019',
    venue: 'Warehouse Stage',
    category: 'Concert',
    price: '€ 60.90',
    description: 'A compact live show experience mapped to realtime seat locking and mock checkout.',
    showtimeId: 'show-002',
    posterClass: 'poster-troh',
  },
  {
    id: 3,
    title: 'Tomorrowland 2019 - Weekend 1 Full Madness Pass',
    date: '24 August, 2019',
    venue: 'Grand Park',
    category: 'Festival',
    price: '€ 95.50',
    description: 'Weekend pass preview with realtime availability and a five-minute reservation timer.',
    showtimeId: 'show-001',
    posterClass: 'poster-lalala',
  },
  {
    id: 4,
    title: 'Katy Perry & Santana - New Orleans Jazz and Heritage',
    date: '10 December, 2019',
    venue: 'Heritage Arena',
    category: 'Jazz',
    price: '€ 99.00',
    description: 'A seated event demo where ticket purchase starts with a temporary distributed lock.',
    showtimeId: 'show-002',
    posterClass: 'poster-get',
  },
]

const selectedShowtime = computed(() =>
  showtimes.value.find((showtime) => showtime.id === selectedShowtimeId.value),
)

const eventByShowtime = computed(() => {
  const map = new Map<string, MarketingEvent>()
  for (const event of marketingEvents) {
    if (!map.has(event.showtimeId)) map.set(event.showtimeId, event)
  }
  return map
})

const categories = computed(() => [
  'All Category',
  ...Array.from(new Set(marketingEvents.map((event) => event.category))),
])

const filteredMarketingEvents = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()

  return marketingEvents.filter((event) => {
    const matchesCategory =
      selectedCategory.value === 'All Category' || event.category === selectedCategory.value
    const matchesSearch =
      !query ||
      event.title.toLowerCase().includes(query) ||
      event.venue.toLowerCase().includes(query) ||
      event.category.toLowerCase().includes(query)

    return matchesCategory && matchesSearch
  })
})

const availableCount = computed(() => seats.value.filter((seat) => seat.status === 'AVAILABLE').length)
const lockedCount = computed(() => seats.value.filter((seat) => seat.status === 'LOCKED').length)
const bookedCount = computed(() => seats.value.filter((seat) => seat.status === 'BOOKED').length)

const selectedSeats = computed(() =>
  selectedSeatIds.value
    .map((id) => seats.value.find((seat) => seat.id === id))
    .filter((seat): seat is Seat => Boolean(seat)),
)

const myLockedSeats = computed(() =>
  seats.value.filter((seat) => seat.status === 'LOCKED' && seat.locked_by === user.value?.id),
)

const selectedLockedSeats = computed(() =>
  selectedSeats.value.filter((seat) => seat.status === 'LOCKED' && seat.locked_by === user.value?.id),
)

const lockExpiresAt = computed(() =>
  selectedLockedSeats.value
    .map((seat) => (seat.lock_expires_at ? new Date(seat.lock_expires_at).getTime() : 0))
    .filter(Boolean)
    .sort((a, b) => a - b)[0],
)

const selectedTotal = computed(() => selectedSeats.value.length * eventPriceNumber(selectedEvent.value))

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
  const cookieToken = readCookie('ticket_token')

  if (callbackToken) {
    token.value = callbackToken
    storage?.setItem('ticket_token', callbackToken)
  } else if (cookieToken) {
    token.value = cookieToken
    storage?.setItem('ticket_token', cookieToken)
    clearCookie('ticket_token')
  }
  if (callbackError) {
    authError.value = `Google login failed: ${callbackError}`
  }
  if (callbackToken || callbackError || window.location.search.includes('token=')) {
    clearUrl()
  }
  timer = window.setInterval(() => {
    nowTick.value = Date.now()
  }, 1000)
  if (token.value) {
    await bootstrap()
  }
})

onUnmounted(() => {
  closeSocket()
  if (timer) window.clearInterval(timer)
})

watch(selectedShowtimeId, async (id) => {
  selectedSeatIds.value = []
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
  clearCookie('ticket_token')
  token.value = ''
  user.value = null
  isBookingView.value = false
  showtimes.value = []
  seats.value = []
  selectedShowtimeId.value = ''
  selectedSeatIds.value = []
  selectedEvent.value = null
  paymentDialogOpen.value = false
  closeSocket()
  clearUrl()
}

function clearUrl() {
  if (typeof window === 'undefined') return
  window.history.replaceState({}, document.title, window.location.pathname)
}

function readCookie(name: string) {
  if (typeof document === 'undefined') return ''
  return (
    document.cookie
      .split('; ')
      .find((row) => row.startsWith(`${name}=`))
      ?.split('=')
      .slice(1)
      .join('=') ?? ''
  )
}

function clearCookie(name: string) {
  if (typeof document === 'undefined') return
  document.cookie = `${name}=; path=/; max-age=0; SameSite=Lax`
}

async function loadShowtimes() {
  showtimes.value = await request<Showtime[]>('/api/showtimes')
  selectedShowtimeId.value = showtimes.value[0]?.id ?? ''
}

async function loadSeats() {
  if (!selectedShowtimeId.value) return
  seats.value = await request<Seat[]>(`/api/showtimes/${selectedShowtimeId.value}/seats`)
}

function openEvent(event: MarketingEvent) {
  selectedEvent.value = event
}

function closeEventDialog() {
  selectedEvent.value = null
}

async function handleEventAction() {
  if (!selectedEvent.value) return
  if (!user.value) {
    loginWithGoogle()
    return
  }
  await openBookingView(selectedEvent.value)
}

async function openBookingView(event: MarketingEvent) {
  selectedEvent.value = event
  isBookingView.value = true
  selectedSeatIds.value = []
  message.value = ''
  if (!showtimes.value.length) {
    await loadShowtimes()
  }
  selectedShowtimeId.value = event.showtimeId || showtimes.value[0]?.id || ''
  if (selectedShowtimeId.value) {
    await loadSeats()
    closeSocket()
    connectSocket()
  }
}

function backToEvents() {
  isBookingView.value = false
  selectedSeatIds.value = []
  message.value = ''
  paymentDialogOpen.value = false
}

function selectSeat(seat: Seat) {
  if (seat.status !== 'AVAILABLE') return
  if (selectedSeatIds.value.includes(seat.id)) {
    selectedSeatIds.value = selectedSeatIds.value.filter((id) => id !== seat.id)
  } else {
    selectedSeatIds.value = [...selectedSeatIds.value, seat.id]
  }
  message.value = ''
}

async function lockSeat(seat: Seat) {
  if (seat.status !== 'AVAILABLE') return
  await request(`/api/showtimes/${selectedShowtimeId.value}/seats/${seat.id}/lock`, {
    method: 'POST',
  })
}

async function startMockPurchase() {
  const seatsToLock = selectedSeats.value.filter((seat) => seat.status === 'AVAILABLE')
  if (!seatsToLock.length) return
  loading.value = true
  message.value = ''
  try {
    for (const seat of seatsToLock) {
      await lockSeat(seat)
    }
    message.value = `${seatsToLock.map((seat) => seat.id).join(', ')} locked for 5 minutes.`
    await loadSeats()
    selectedSeatIds.value = seatsToLock.map((seat) => seat.id)
    paymentDialogOpen.value = true
  } catch (error) {
    message.value = error instanceof Error ? error.message : 'Unable to lock selected seats'
    await loadSeats()
  } finally {
    loading.value = false
  }
}

async function cancelPayment() {
  const lockedSeats = selectedLockedSeats.value
  loading.value = true
  message.value = ''
  try {
    for (const seat of lockedSeats) {
      await request(`/api/showtimes/${selectedShowtimeId.value}/seats/${seat.id}/release`, {
        method: 'POST',
      })
    }
    paymentDialogOpen.value = false
    selectedSeatIds.value = []
    message.value = lockedSeats.length ? 'Selected seats were released.' : ''
    await loadSeats()
  } catch (error) {
    message.value = error instanceof Error ? error.message : 'Unable to release selected seats'
    await loadSeats()
  } finally {
    loading.value = false
  }
}

async function confirmBooking() {
  const seatsToConfirm = selectedLockedSeats.value
  if (!seatsToConfirm.length || !selectedShowtimeId.value) return
  loading.value = true
  message.value = ''
  try {
    for (const seat of seatsToConfirm) {
      await request('/api/bookings/confirm', {
        method: 'POST',
        body: JSON.stringify({ showtime_id: selectedShowtimeId.value, seat_id: seat.id }),
      })
    }
    message.value = `Booking confirmed for ${seatsToConfirm.map((seat) => seat.id).join(', ')}.`
    paymentDialogOpen.value = false
    selectedSeatIds.value = []
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
      selectedSeatIds.value = selectedSeatIds.value.filter((id) => {
        const seat = seats.value.find((item) => item.id === id)
        return seat && seat.status !== 'BOOKED' && (seat.status !== 'LOCKED' || seat.locked_by === user.value?.id)
      })
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
  const seconds = Math.max(0, Math.ceil((new Date(seat.lock_expires_at).getTime() - nowTick.value) / 1000))
  const minutes = Math.floor(seconds / 60)
  return `${minutes}:${String(seconds % 60).padStart(2, '0')}`
}

function lockCountdownFromTime(value?: number) {
  if (!value) return ''
  const seconds = Math.max(0, Math.ceil((value - nowTick.value) / 1000))
  const minutes = Math.floor(seconds / 60)
  return `${minutes}:${String(seconds % 60).padStart(2, '0')}`
}

function eventPriceNumber(event: MarketingEvent | null) {
  if (!event) return 0
  return Number(event.price.replace(/[^\d.]/g, '')) || 0
}

function formatMoney(value: number) {
  return new Intl.NumberFormat('en-US', { style: 'currency', currency: 'EUR' }).format(value)
}

function bookingEventName(booking: Booking) {
  return eventByShowtime.value.get(booking.showtime_id)?.title || booking.showtime_id
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

        <button v-if="!user" class="google-button nav-google-button" type="button" :disabled="loading" @click="loginWithGoogle">
          <span class="google-mark">G</span>
          Continue with Google
        </button>

        <div v-else class="user-chip">
          <img v-if="user.picture" :src="user.picture" alt="" />
          <span>{{ user.name || user.email }}</span>
          <strong>{{ user.role }}</strong>
        </div>

        <button v-if="user" class="ghost-button" type="button" @click="logout">Logout</button>
      </header>

      <section v-if="!isBookingView" class="marketing-shell" aria-labelledby="login-title">
        <section class="marketing-hero">
          <h1 id="login-title">Selling Electronic Tickets On The Webpage Ticket Online</h1>

          <form class="search-showcase" role="search" @submit.prevent>
            <label class="search-category" for="event-category">
              <select id="event-category" v-model="selectedCategory" aria-label="Category">
                <option v-for="category in categories" :key="category">{{ category }}</option>
              </select>
            </label>
            <input
              v-model="searchQuery"
              class="search-copy"
              type="search"
              placeholder="Search Festival, Ticket Or Club Name..."
              aria-label="Search events"
            />
            <button class="search-button" type="submit" aria-label="Search">
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <circle cx="10.5" cy="10.5" r="6.5"></circle>
                <path d="M16 16L21 21"></path>
              </svg>
            </button>
          </form>
        </section>

        <section class="marketing-section" aria-labelledby="top-events-title">
          <div class="marketing-header">
            <h2 id="top-events-title">Week Top Events</h2>
          </div>

          <div class="marketing-grid">
            <article
              v-for="event in filteredMarketingEvents"
              :key="event.id"
              class="marketing-card"
              tabindex="0"
              role="button"
              @click="openEvent(event)"
              @keydown.enter="openEvent(event)"
            >
              <div class="marketing-poster" :class="event.posterClass">
                <span class="poster-price">{{ event.price }}</span>
              </div>
              <div class="marketing-body">
                <h3>{{ event.title }}</h3>
                <p>{{ event.date }}</p>
              </div>
            </article>
          </div>
          <p v-if="!filteredMarketingEvents.length" class="empty-events">No events found.</p>
        </section>

        <p v-if="authError" class="error-text marketing-error">{{ authError }}</p>
      </section>

      <section v-else class="booking-layout">
        <aside class="event-summary-panel" aria-label="Event details">
          <button class="ghost-button back-button" type="button" @click="backToEvents">Back to events</button>
          <div v-if="selectedEvent" class="summary-poster marketing-poster" :class="selectedEvent.posterClass">
            <span class="poster-price">{{ selectedEvent.price }}</span>
          </div>
          <div class="panel-heading">
            <p class="eyebrow">Selected Event</p>
            <h2>{{ selectedEvent?.title || 'Ticket Online' }}</h2>
            <span>{{ selectedEvent?.date }} · {{ selectedEvent?.venue }}</span>
            <span>{{ selectedEvent?.description }}</span>
          </div>
        </aside>

        <section class="seat-panel" aria-labelledby="seat-title">
          <div class="panel-heading seat-heading">
            <div>
              <p class="eyebrow">Seat Map</p>
              <h2 id="seat-title">{{ selectedEvent?.title }}</h2>
              <span>{{ selectedShowtime?.theater }} · {{ selectedShowtime ? formatDate(selectedShowtime.starts_at) : '' }}</span>
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
                :class="[
                  seat.status.toLowerCase(),
                  {
                    mine: seat.locked_by === user?.id,
                    selected: selectedSeatIds.includes(seat.id),
                  },
                ]"
                type="button"
                :disabled="seat.status === 'BOOKED' || (seat.status === 'LOCKED' && seat.locked_by !== user?.id) || loading"
                :title="seat.status"
                @click="selectSeat(seat)"
              >
                <span>{{ seat.id }}</span>
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
              <strong>{{ selectedSeats.length ? `${selectedSeats.length} selected` : 'No selected seats' }}</strong>
              <span v-if="selectedLockedSeats.length">Expires in {{ lockCountdownFromTime(lockExpiresAt) }}</span>
              <span v-else>Choose one or more available seats, then buy to lock them for 5 minutes.</span>
            </div>
          </div>

          <p v-if="message" class="status-message">{{ message }}</p>
        </section>

        <aside class="booking-detail-panel" :class="{ visible: selectedSeats.length }" aria-label="Selected seat details">
          <div class="panel-heading">
            <p class="eyebrow">Checkout</p>
            <h2>{{ selectedSeats.length ? `${selectedSeats.length} seat${selectedSeats.length > 1 ? 's' : ''}` : 'Select seats' }}</h2>
          </div>
          <div class="detail-event">
            <strong>{{ selectedEvent?.title }}</strong>
            <span>{{ selectedEvent?.date }} · {{ selectedEvent?.venue }}</span>
          </div>
          <div v-if="selectedSeats.length" class="seat-summary">
            <span>Seats</span>
            <b>{{ selectedSeats.map((seat) => seat.id).join(', ') }}</b>
            <span>Status</span>
            <b>{{ selectedLockedSeats.length ? 'LOCKED' : 'SELECTED' }}</b>
            <span>Total</span>
            <b>{{ formatMoney(selectedTotal) }}</b>
          </div>
          <p v-if="!selectedSeats.length" class="detail-muted">Choose available seats from the map to review your ticket.</p>
          <p v-else-if="selectedLockedSeats.length" class="detail-muted">Seats are locked for you. Complete the mock payment before {{ lockCountdownFromTime(lockExpiresAt) }}.</p>
          <p v-else class="detail-muted">Buy Ticket will lock your selected seats for 5 minutes.</p>
          <button
            v-if="selectedSeats.length && !selectedLockedSeats.length"
            class="primary-button detail-action"
            type="button"
            :disabled="loading"
            @click="startMockPurchase"
          >
            Buy Ticket
          </button>
        </aside>

        <section v-if="user?.role === 'ADMIN'" class="admin-panel" aria-label="Admin dashboard">
          <div class="panel-heading">
            <p class="eyebrow">Admin</p>
            <h2>Bookings & audit logs</h2>
          </div>
          <button class="ghost-button" type="button" @click="loadAdminData">Refresh</button>

          <div class="admin-columns">
            <div>
              <h3>Bookings</h3>
              <div class="admin-booking-table">
                <article v-for="booking in bookings" :key="booking.id" class="admin-booking-row">
                  <b>{{ booking.user_name || 'Google User' }}</b>
                  <span>{{ booking.user_email }}</span>
                  <strong>{{ bookingEventName(booking) }}</strong>
                  <em>Seat {{ booking.seat_id }} · {{ booking.status }}</em>
                </article>
              </div>
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

      <div v-if="selectedEvent && !isBookingView" class="event-dialog-backdrop" @click.self="closeEventDialog">
        <section class="event-dialog" role="dialog" aria-modal="true" :aria-label="selectedEvent.title">
          <button class="dialog-close" type="button" aria-label="Close" @click="closeEventDialog">x</button>
          <div class="dialog-poster marketing-poster" :class="selectedEvent.posterClass">
            <span class="poster-price">{{ selectedEvent.price }}</span>
          </div>
          <div class="dialog-content">
            <p class="eyebrow">{{ selectedEvent.category }}</p>
            <h2>{{ selectedEvent.title }}</h2>
            <p>{{ selectedEvent.description }}</p>
            <div class="dialog-meta">
              <span>{{ selectedEvent.date }}</span>
              <span>{{ selectedEvent.venue }}</span>
              <span>{{ selectedEvent.price }}</span>
            </div>
            <button class="primary-button dialog-action" type="button" :disabled="loading" @click="handleEventAction">
              {{ user ? 'Buy Ticket' : 'Continue with Google' }}
            </button>
          </div>
        </section>
      </div>

      <div v-if="paymentDialogOpen" class="payment-dialog-backdrop">
        <section class="payment-dialog" role="dialog" aria-modal="true" aria-label="Confirm payment">
          <p class="eyebrow">Mock Payment</p>
          <h2>Confirm your ticket payment</h2>
          <div class="payment-summary">
            <span>{{ selectedEvent?.title }}</span>
            <strong>{{ selectedSeats.map((seat) => seat.id).join(', ') }}</strong>
            <b>{{ formatMoney(selectedTotal) }}</b>
            <em>Expires in {{ lockCountdownFromTime(lockExpiresAt) }}</em>
          </div>
          <div class="payment-actions">
            <button class="ghost-button" type="button" :disabled="loading" @click="cancelPayment">Cancel</button>
            <button class="primary-button" type="button" :disabled="loading" @click="confirmBooking">
              Confirm Payment
            </button>
          </div>
        </section>
      </div>
    </section>
  </main>
</template>
