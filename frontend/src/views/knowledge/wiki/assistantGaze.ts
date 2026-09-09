/**
 * Gaze-following math for the wiki assistant mascot.
 *
 * Pure functions so the policy is unit-testable: the pupils translate toward
 * the pointer with a magnitude that saturates at maxOffset and eases to zero
 * when the pointer is right on top of the ball (avoids jitter at the center).
 */

export interface Point {
  x: number
  y: number
}

/**
 * Pupil offset toward the pointer.
 * - direction: unit vector from the ball center to the pointer
 * - magnitude: min(maxOffset, distance / distanceScale) — grows with
 *   distance up to maxOffset, so the mascot "locks on" once the pointer is
 *   far and barely moves when it hovers the ball itself
 */
export function pupilOffset(
  ballCenter: Point,
  pointer: Point,
  maxOffset = 3,
  distanceScale = 18,
): Point {
  const dx = pointer.x - ballCenter.x
  const dy = pointer.y - ballCenter.y
  const dist = Math.hypot(dx, dy)
  if (dist < 1 || maxOffset <= 0) return { x: 0, y: 0 }
  const magnitude = Math.min(maxOffset, dist / distanceScale)
  return {
    x: (dx / dist) * magnitude,
    y: (dy / dist) * magnitude,
  }
}
