<script setup lang="ts"></script>

<template>
  <div class="min-h-screen bg-gray-100 flex items-center justify-center">
    <div class="bg-white p-8 rounded-xl shadow-lg w-96">
      <h1 class="text-3xl font-bold text-blue-600 mb-4">
        Tailwind CSS Works 🎉
      </h1>

      <p class="text-gray-600 mb-4">
        Vue + Tailwind พร้อมใช้งานแล้ว
      </p>

      <div class="flex gap-2 mb-4">
        <span class="bg-green-500 text-white px-3 py-1 rounded-full text-sm">
          Vue 3
        </span>

        <span class="bg-blue-500 text-white px-3 py-1 rounded-full text-sm">
          Tailwind
        </span>
      </div>

      <button
        class="bg-indigo-600 hover:bg-indigo-700 text-white px-4 py-2 rounded-lg transition"
      >
        Test Button
      </button>
    </div>
  </div>
</template>

<style scoped></style>
