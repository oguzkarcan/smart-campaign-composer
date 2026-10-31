<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { createCampaign } from '@/api/client'
import { CHANNELS, type Channel } from '@/api/types'
import { channelLabel } from '@/lib/format'
import { t } from '@/lib/i18n'
import ChannelIcon from './ChannelIcon.vue'
import ErrorAlert from './ErrorAlert.vue'

const MAX_BRIEF = 2000 // campaign.MaxBriefLength

const examples = [
  // The case study's example brief.
  "Son 30 günde sepetine ürün ekleyip alışveriş yapmayan İstanbul'daki kullanıcılara %30 indirim hatırlatması gönder. Samimi ama kısa olsun.",
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
  <form class="space-y-5" @submit.prevent="submit">
    <!-- The prompt box: a gradient hairline around the brief and its controls. -->
    <div
      class="rounded-2xl bg-linear-to-br from-indigo-400 via-violet-300 to-sky-300 p-px shadow-lg shadow-indigo-500/10 transition-shadow has-[textarea:focus]:ring-4 has-[textarea:focus]:ring-indigo-100"
    >
      <div class="rounded-[calc(1rem-1px)] bg-white">
        <label for="brief" class="flex items-center gap-2 px-5 pt-4 text-xs font-bold tracking-wider text-indigo-600 uppercase">
          <span aria-hidden="true">✦</span>{{ t('new.brief') }}
        </label>
        <textarea
          id="brief"
          v-model="brief"
          rows="5"
          :disabled="submitting"
          :placeholder="t('new.placeholder')"
          class="block w-full resize-none border-0 bg-transparent px-5 py-3 text-base leading-relaxed text-slate-900 placeholder:text-slate-400 focus:ring-0 focus:outline-none disabled:text-slate-500"
          @keydown.meta.enter="submit"
          @keydown.ctrl.enter="submit"
        />
        <div class="flex flex-wrap items-center justify-between gap-3 border-t border-slate-100 px-4 py-3">
          <fieldset class="flex flex-wrap items-center gap-2">
            <legend class="sr-only">{{ t('new.channels') }}</legend>
            <span class="mr-1 text-xs font-medium text-slate-500" aria-hidden="true">{{ t('new.channels') }}</span>
            <label
              v-for="ch in CHANNELS"
              :key="ch"
              class="flex cursor-pointer items-center gap-1.5 rounded-full px-3 py-1 text-sm font-medium ring-1 transition has-focus-visible:ring-2 has-focus-visible:ring-indigo-400"
              :class="
                channels.includes(ch)
                  ? 'bg-indigo-50 text-indigo-700 ring-indigo-300'
                  : 'bg-white text-slate-500 ring-slate-200 hover:text-slate-700'
              "
            >
              <input v-model="channels" type="checkbox" :value="ch" :disabled="submitting" class="sr-only" />
              <ChannelIcon :channel="ch" class="size-4" />
              {{ channelLabel(ch) }}
              <span v-if="channels.includes(ch)" aria-hidden="true" class="text-xs">✓</span>
            </label>
          </fieldset>
          <div class="flex items-center gap-3">
            <span :class="briefLength > MAX_BRIEF ? 'text-rose-600' : 'text-slate-400'" class="font-mono text-xs tabular-nums">
              {{ briefLength }} / {{ MAX_BRIEF }}
            </span>
            <button
              type="submit"
              :disabled="!canSubmit"
              class="inline-flex items-center gap-2 rounded-xl bg-linear-to-r from-indigo-600 to-violet-600 px-4 py-2 text-sm font-semibold text-white shadow-md shadow-indigo-500/20 hover:from-indigo-700 hover:to-violet-700 disabled:cursor-not-allowed disabled:opacity-50"
            >
              <span v-if="submitting" class="size-3.5 animate-spin rounded-full border-2 border-white/40 border-t-white" aria-hidden="true" />
              {{ submitting ? t('new.submitting') : t('new.submit') }}
            </button>
          </div>
        </div>
      </div>
    </div>
    <p v-if="channels.length === 0" class="text-sm text-rose-600">{{ t('new.pickChannel') }}</p>
    <p v-if="submitting" class="text-sm text-slate-500">{{ t('new.submittingHint') }}</p>

    <ErrorAlert v-if="error" :error="error" :title="t('new.createError')" />

    <div class="space-y-2">
      <p class="text-xs font-semibold tracking-wider text-slate-500 uppercase">{{ t('new.try') }}</p>
      <div class="grid gap-3 md:grid-cols-3">
        <button
          v-for="ex in examples"
          :key="ex"
          type="button"
          class="group flex gap-3 rounded-xl border bg-white p-4 text-left text-sm shadow-xs transition hover:-translate-y-px hover:border-indigo-300 hover:shadow-md disabled:opacity-50"
          :class="brief === ex ? 'border-indigo-400 ring-2 ring-indigo-100' : 'border-slate-200'"
          :title="ex"
          :disabled="submitting"
          @click="brief = ex"
        >
          <span
            class="flex size-7 shrink-0 items-center justify-center rounded-lg bg-indigo-50 text-indigo-600 group-hover:bg-indigo-100"
            aria-hidden="true"
          >
            ✦
          </span>
          <span class="line-clamp-3 leading-snug text-slate-700 group-hover:text-slate-900">{{ ex }}</span>
        </button>
      </div>
    </div>
  </form>
</template>
