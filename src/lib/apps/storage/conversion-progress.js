// Only the exact journal entry can establish a conversion's terminal result.
export function conversionResult(operations, id) {
  const list = Array.isArray(operations) ? operations : operations?.operations || [];
  const op = list.find(item => item.id === id && item.type === 'convert_profile');
  if (op?.status === 'completed') return { phase: 'done' };
  if (['failed', 'cancelled', 'rolled_back'].includes(op?.status)) {
    return { phase: 'error', error: op.error || 'La conversión no se completó. Revisa el pool en Almacenamiento.' };
  }
  return { phase: 'converting' };
}
