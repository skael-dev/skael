// - Consider using p5 vectors efficiently

// For smooth animation:
// - Aim for 60fps
// - Profile if things are slow
// - Consider reducing particle counts or simplifying calculations

// ============================================================================
// 6. UTILITY FUNCTIONS
// ============================================================================

// Color utilities
function hexToRgb(hex) {
    const result = /^#?([a-f\d]{2})([a-f\d]{2})([a-f\d]{2})$/i.exec(hex);
    return result ? {
        r: parseInt(result[1], 16),
        g: parseInt(result[2], 16),
        b: parseInt(result[3], 16)
    } : null;
}

function colorFromPalette(index) {
    return params.colorPalette[index % params.colorPalette.length];
}

// Mapping and easing
