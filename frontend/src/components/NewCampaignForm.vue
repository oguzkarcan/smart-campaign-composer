<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { createCampaign } from '@/api/client'
import { CHANNELS, type Channel } from '@/api/types'
import { channelLabels } from '@/lib/format'
import ErrorAlert from './ErrorAlert.vue'

const MAX_BRIEF = 2000 // campaign.MaxBriefLength

const examples = [
  'Win back users in İstanbul and Ankara who added to cart in the last 7 days but did not purchase.',
  'Thank English-speaking customers who purchased at least twice in the last 30 days.',
]

const router = useRouter()
const brief = ref('')
const channels = ref<Channel[]>(['sms', 'email'])
const submitting = ref(false)
const error = ref<unknown>(null)

const briefLength = computed(() => [...brief.value.trim()].length) // code points, like the API
const canSubmit = computed(
  () => !submitting.value && briefLength.value > 0 && briefLength.value <= MAX_BRIEF && channels.value.length > 0,
)

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  error.value = null
  try {
    // Keep the catalog order so the API stores channels the same way every time.
    const selected = CHANNELS.filter((ch) => channels.value.includes(ch))
    const c = await createCampaign(brief.value.trim(), selected)
    await router.push({ name: 'campaign', params: { id: c.id } })
  } catch (e) {
    error.value = e
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <form class="space-y-4 rounded-xl border border-slate-200 bg-white p-6 shadow-sm" @submit.prevent="submit">
    <div>
      <label for="brief" class="block text-sm font-medium">Campaign brief</label>
      <textarea
        id="brief"
        v-model="brief"
        rows="4"
        :disabled="submitting"
        placeholder="Who should receive it, and what should it say?"
        class="mt-1 w-full rounded-lg border border-slate-300 px-3 py-2 focus:border-indigo-500 focus:ring-2 focus:ring-indigo-200 focus:outline-none disabled:bg-slate-50"
        @keydown.meta.enter="submit"
        @keydown.ctrl.enter="submit"
      />
      <div class="mt-1 flex flex-wrap items-start justify-between gap-2 text-xs text-slate-500">
        <div class="flex flex-wrap gap-2">
          <span>Try:</span>
          <button
            v-for="ex in examples"
            :key="ex"
            type="button"
            class="max-w-xs truncate text-indigo-600 hover:underline"
            :title="ex"
            :disabled="submitting"
            @click="brief = ex"
          >
            {{ ex }}
          </button>
        </div>
        <span :class="{ 'text-rose-600': briefLength > MAX_BRIEF }" class="tabular-nums">
          {{ briefLength }} / {{ MAX_BRIEF }}
        </span>
      </div>
    </div>

    <fieldset>
      <legend class="text-sm font-medium">Channels</legend>
      <div class="mt-2 flex gap-6">
        <label v-for="ch in CHANNELS" :key="ch" class="flex items-center gap-2 text-sm">
          <input
            v-model="channels"
            type="checkbox"
            :value="ch"
            :disabled="submitting"
            class="size-4 rounded border-slate-300 text-indigo-600"
          />
          {{ channelLabels[ch] }}
        </label>
      </div>
      <p v-if="channels.length === 0" class="mt-1 text-xs text-rose-600">Pick at least one channel.</p>
    </fieldset>

    <ErrorAlert v-if="error" :error="error" title="Could not create the campaign" />

    <div class="flex items-center gap-3">
      <button
        type="submit"
        :disabled="!canSubmit"
        class="rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700 disabled:cursor-not-allowed disabled:opacity-50"
      >
        {{ submitting ? 'Building audience…' : 'Build audience' }}
      </button>
      <span v-if="submitting" class="text-sm text-slate-500">Claude is turning the brief into a segment rule.</span>
    </div>
  </form>
</template>
