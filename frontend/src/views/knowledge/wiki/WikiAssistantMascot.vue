<template>
  <!--
    WeKnora wiki assistant mascot — generated PNG skin (GPT Image 2 artwork,
    script-aligned layers). SKIN PROTOCOL: the skin is the two PNGs in
    src/assets/mascot/ (assistant-base + assistant-pupils) sharing one
    512×512 canvas with the pupils pre-centered on the base's eye whites;
    replacing both files with a new same-canvas pair re-skins the widget.
    The parent still drives gaze via pupilDx/pupilDy (96-unit mascot space,
    clamped to the asset's eye-white slack below) and blinking via the
    .is-blinking class on an ancestor.
    NOTE: the gaze translate lives on a wrapper div and the blink animation
    on the img itself — animating the same element that carries the inline
    transform would let the keyframes override it and snap the pupils back
    to the origin mid-gaze.
  -->
  <div class="mascot" role="img" aria-hidden="true">
    <img class="mascot-base" :src="baseSrc" alt="" draggable="false" />
    <div class="mascot-pupil-move" :style="pupilStyle">
      <img class="mascot-pupils" :src="pupilSrc" alt="" draggable="false" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import baseSrc from '@/assets/mascot/assistant-base.png'
import pupilSrc from '@/assets/mascot/assistant-pupils.png'

const props = defineProps<{
  /** gaze offset in px inside the 96-unit mascot space */
  pupilDx?: number
  pupilDy?: number
}>()

// Eye-white slack of the shipped Q-version skin (big eyes, 512 canvas),
// measured after the eye enlargement pass: eyes ~78-85×99-103, pupils
// ~45-51 → slack x ≈17px / y ≈25px of 512 → 96-unit space (×96/512),
// minus a small margin so pupils kiss the white edge, never cross it.
const X_LIMIT = 3.0
const Y_LIMIT = 4.4

function clampUnits(v: number, lim: number): number {
  return Math.max(-lim, Math.min(lim, v))
}

// translate() percentages resolve against the element's own box (the full
// mascot square), so units/96×100 % reproduces the SVG-era px math at any
// rendered size.
const pupilStyle = computed(() => ({
  transform: `translate(${((clampUnits(props.pupilDx ?? 0, X_LIMIT) / 96) * 100).toFixed(3)}%, ${((clampUnits(props.pupilDy ?? 0, Y_LIMIT) / 96) * 100).toFixed(3)}%)`,
}))
</script>

<style scoped>
.mascot { position: relative; width: 100%; height: 100%; display: block; }
.mascot-base, .mascot-pupil-move, .mascot-pupils {
  position: absolute; inset: 0; width: 100%; height: 100%; display: block;
  user-select: none; -webkit-user-drag: none;
}
.mascot-pupil-move { transition: transform 0.12s ease-out; will-change: transform; }
/* the blink squash collapses toward the eye line (~51% of the canvas) */
.mascot-pupils { transform-origin: 50% 51%; }
/* the widget toggles .is-blinking on the ball wrapper */
:global(.is-blinking) .mascot-pupils { animation: mascot-blink 0.18s ease-in-out; }
@keyframes mascot-blink {
  0%, 100% { transform: scaleY(1); }
  50% { transform: scaleY(0.12); }
}
@media (prefers-reduced-motion: reduce) {
  .mascot-pupil-move { transition: none; }
  :global(.is-blinking) .mascot-pupils { animation: none; }
}
</style>
