<script setup lang="ts">
import { computed, reactive, ref } from 'vue'

type TicketStatus = 'Available' | 'Selling fast' | 'Sold out'

interface EventTicket {
  id: number
  title: string
  date: string
  venue: string
  category: string
  price: number
  status: TicketStatus
  accent: string
  posterClass: string
}

const events = reactive<EventTicket[]>([
  {
    id: 1,
    title: 'Official After Party Awakenings Festival 2018',
    date: '10 December, 2019',
    venue: 'Bangkok Hall',
    category: 'Festival',
    price: 73.54,
    status: 'Available',
    accent: '#b9d7d6',
    posterClass: 'poster-abstract',
  },
  {
    id: 2,
    title: 'Dekmantel Festival 2019 - Wednesday',
    date: '12 September, 2019',
    venue: 'Warehouse Stage',
    category: 'Concert',
    price: 60.9,
    status: 'Selling fast',
    accent: '#0e243a',
    posterClass: 'poster-tron',
  },
  {
    id: 3,
    title: 'Tomorrowland 2019 - Weekend 1 Full Madness Pass',
    date: '24 August, 2019',
    venue: 'Grand Park',
    category: 'Festival',
    price: 95.5,
    status: 'Available',
    accent: '#f4e6c4',
    posterClass: 'poster-vinyl',
  },
  {
    id: 4,
    title: 'Katy Perry & Santana - New Orleans Jazz and Heritage',
    date: '10 December, 2019',
    venue: 'Heritage Arena',
    category: 'Jazz',
    price: 99,
    status: 'Sold out',
    accent: '#de4054',
    posterClass: 'poster-red',
  },
])

const search = ref('')
const selectedCategory = ref('All Category')

const categories = computed(() => ['All Category', ...new Set(events.map((event) => event.category))])

const filteredEvents = computed(() => {
  const query = search.value.trim().toLowerCase()

  return events.filter((event) => {
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

function formatPrice(price: number) {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'EUR',
    minimumFractionDigits: 2,
  }).format(price)
}
</script>

<template>
  <main class="app-shell">
    <section class="ticket-window">
      <header class="topbar">
        <button class="brand" type="button" aria-label="Ticketsy home">
          <span class="brand-mark"></span>
          <span>Ticketsy</span>
        </button>

        <nav class="main-nav" aria-label="Main navigation">
          <a href="#events">Help Centre</a>
          <a href="#events">About Us</a>
          <a href="#events">Corporate Service</a>
        </nav>

        <div class="top-actions">
          <button class="ghost-button" type="button">
            <span class="button-icon">+</span>
            Upload Ticket
          </button>
          <button class="sign-button" type="button">Sign In</button>
          <button class="cart-button" type="button" aria-label="Cart">
            <span class="cart-shape"></span>
            <span class="cart-count">0</span>
          </button>
        </div>
      </header>

      <section class="hero" aria-labelledby="hero-title">
        <h1 id="hero-title">Selling Electronic Tickets On The Webpage Ticketsy</h1>

        <div class="search-box">
          <select v-model="selectedCategory" aria-label="Category">
            <option v-for="category in categories" :key="category">{{ category }}</option>
          </select>
          <input v-model="search" type="search" placeholder="Search Festival, Ticket Or Club Name..." />
          <button type="button" aria-label="Search">
            <span class="search-icon"></span>
          </button>
        </div>
      </section>

      <section id="events" class="events-section" aria-labelledby="events-title">
        <div class="section-heading">
          <h2 id="events-title">Week Top Events</h2>
        </div>

        <div class="event-grid">
          <article v-for="event in filteredEvents" :key="event.id" class="event-card">
            <div class="poster" :class="event.posterClass" :style="{ '--accent': event.accent }">
              <span class="poster-price">{{ formatPrice(event.price) }}</span>
              <span class="poster-title">{{ event.category }}</span>
              <span class="poster-shape"></span>
            </div>

            <div class="event-body">
              <div class="event-meta">
                <span>{{ event.status }}</span>
                <span>{{ event.venue }}</span>
              </div>
              <h3>{{ event.title }}</h3>
              <p>
                <span class="calendar-icon"></span>
                {{ event.date }}
              </p>
              <button class="buy-button" type="button" :disabled="event.status === 'Sold out'">
                {{ event.status === 'Sold out' ? 'Sold Out' : 'Buy Ticket' }}
              </button>
            </div>
          </article>
        </div>
      </section>
    </section>
  </main>
</template>
