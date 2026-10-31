<script setup lang="ts">
import { computed, onUnmounted, reactive, ref } from 'vue'
import { approveCampaign, generateContent } from '@/api/client'
import type { Campaign, Channel, Content } from '@/api/types'
import { channelLabels, formatNumber } from '@/lib/format'
import ChannelEditor from './ChannelEditor.vue'
import ErrorAlert from './ErrorAlert.vue'

const props = defineProps<{ campaign: Campaign }>()
const emit = defineEmits<{ update: [campaign: Campaign]; busy: [busy: boolean] }>()

const generating = ref(false)
const attempt = ref(1)
const streamed = ref('')
const generateError = ref<unknown>(null)
let controller: AbortController | undefined

const approving = ref(false)
const approveError = ref<unknown>(null)

const editorStates = reactive<Partial<Record<Channel, { dirty: boolean; valid: boolean }>>>({})

const status = computed(() => props.campaign.status)
const contents = computed(() => {
  const byChannel = new Map((props.campaign.contents ?? []).map((c) => [c.channel, c]))
  return props.campaign.channels.map((ch) => byChannel.get(ch)).filter((c): c is Content => !!c)
})
const canGenerate = computed(() => status.value === 'audience_ready' || status.value === 'content_ready')
const reachable = computed(() => {
  const a = props.campaign.audience
  return a ? props.campaign.channels.reduce((n, ch) => n + a[ch], 0) : 0
})
const editable = computed(() => status.value === 'content_ready' && !generating.value && !approving.value)
const anyDirty = computed(() => Object.values(editorStates).some((s) => s?.dirty))
const allValid = computed(() => Object.values(editorStates).every((s) => s?.valid))
const canApprove = computed(() => editable.value && contents.value.length > 0 && !anyDirty.value && allValid.value)
const approveHint = computed(() => {
  if (anyDirty.value) return 'Save or discard your changes before approving.'
  if (!allValid.value) return 'Fix the problems above before approving.'
  return ''
})

async function generate() {
  if (!canGenerate.value || generating.value) return
  if ((anyDirty.value || contents.value.some((c) => c.human_edited)) && !confirm('New messages replace your edits. Continue?')) return
  controller = new AbortController()
  generating.value = true
  emit('busy', true)
  generateError.value = null
  approveError.value = null
  attempt.value = 1
  streamed.value = ''
  try {
    const c = await generateContent(props.campaign.id, {
      signal: controller.signal,
      onDelta(n, text) {
        if (n !== attempt.value) {
          attempt.value = n // a repair round starts over
          streamed.value = ''
        }
        streamed.value += text
      },
    })
    emit('update', c)
  } catch (e) {
    if (!controller.signal.aborted) generateError.value = e
  } finally {
    generating.value = false
    emit('busy', false)
  }
}

function cancel() {
  controller?.abort()
}
onUnmounted(cancel)

async function approve() {
  if (!canApprove.value) return
  const a = props.campaign.audience
  const parts = props.campaign.channels.map((ch) => `${formatNumber(a?.[ch] ?? 0)} ${channelLabels[ch]}`)
  if (!confirm(`Approve and send ${parts.join(' + ')} messages? The content is locked after this.`)) return
  approving.value = true
  approveError.value = null
  try {
    emit('update', await approveCampaign(props.campaign.id))
  } catch (e) {
    approveError.value = e
  } finally {
    approving.value = false
  }
}
</script>

<template>
  <section class="space-y-4" aria-labelledby="content-heading">
    <div class="flex items-center justify-between gap-4">
      <h2 id="content-heading" class="text-lg font-semibold">Messages</h2>
      <button
        v-if="canGenerate && contents.length && !generating"
        type="button"
        :disabled="approving"
        class="rounded-lg border border-slate-300 bg-white px-3 py-1.5 text-sm font-medium hover:bg-slate-50 disabled:opacity-50"
        @click="generate"
      >
        Regenerate messages
      </button>
    </div>

    <ErrorAlert v-if="generateError" :error="generateError" title="Claude could not write valid messages" />

    <!-- Live generation: the model's raw output as it streams in. -->
    <div v-if="generating" class="space-y-2 rounded-xl border border-indigo-200 bg-white p-4" aria-live="polite">
      <div class="flex items-center justify-between gap-2 text-sm">
        <span class="flex items-center gap-2 text-indigo-700">
          <span class="size-2 animate-pulse rounded-full bg-indigo-500" />
          {{ attempt === 1 ? 'Claude is writing the messages…' : `Fixing validation problems (attempt ${attempt})…` }}
        </span>
        <button type="button" class="text-slate-500 hover:text-slate-800" @click="cancel">Cancel</button>
      </div>
      <pre class="max-h-72 overflow-auto rounded-lg bg-slate-900 p-3 font-mono text-xs whitespace-pre-wrap text-slate-100">{{ streamed || ' ' }}<span class="animate-pulse">▍</span></pre>
    </div>

    <template v-else-if="!contents.length">
      <div v-if="canGenerate" class="rounded-xl border border-dashed border-slate-300 bg-white p-6 text-center">
        <template v-if="reachable > 0">
          <p class="text-slate-600">Next, let Claude write the messages for this audience.</p>
          <button
            type="button"
            class="mt-3 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-700"
            @click="generate"
          >
            Generate messages
          </button>
        </template>
        <p v-else class="text-slate-600">
          Nobody in this audience can be reached on the chosen channels. Change the brief or the channels.
        </p>
      </div>
      <p v-else class="text-sm text-slate-500">Messages are written once the audience is ready.</p>
    </template>

    <template v-if="contents.length && !generating">
      <div class="grid gap-4" :class="{ 'lg:grid-cols-2': contents.length > 1 }">
        <ChannelEditor
          v-for="c in contents"
          :key="c.channel"
          :campaign-id="campaign.id"
          :channel="c.channel"
          :saved="c"
          :variable-lengths="campaign.variable_lengths ?? {}"
          :limits="campaign.limits"
          :readonly="!editable"
          @saved="emit('update', $event)"
          @state="(ch, s) => (editorStates[ch] = s)"
        />
      </div>

      <ErrorAlert v-if="approveError" :error="approveError" title="Could not approve" />

      <div v-if="status === 'content_ready'" class="flex flex-wrap items-center gap-3">
        <button
          type="button"
          :disabled="!canApprove"
          class="rounded-lg bg-emerald-600 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-50"
          @click="approve"
        >
          {{ approving ? 'Approving…' : 'Approve and send' }}
        </button>
        <span v-if="approveHint" class="text-sm text-slate-500">{{ approveHint }}</span>
      </div>
      <p v-else class="text-sm text-slate-500">This content was approved and is locked.</p>
    </template>
  </section>
</template>
