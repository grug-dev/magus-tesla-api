---
name: Apex Performance
colors:
  surface: '#121414'
  surface-dim: '#121414'
  surface-bright: '#37393a'
  surface-container-lowest: '#0c0f0f'
  surface-container-low: '#1a1c1c'
  surface-container: '#1e2020'
  surface-container-high: '#282a2b'
  surface-container-highest: '#333535'
  on-surface: '#e2e2e2'
  on-surface-variant: '#e7bdb8'
  inverse-surface: '#e2e2e2'
  inverse-on-surface: '#2f3131'
  outline: '#ae8884'
  outline-variant: '#5d3f3c'
  surface-tint: '#ffb4ac'
  primary: '#ffb4ac'
  on-primary: '#690007'
  primary-container: '#e82127'
  on-primary-container: '#140000'
  inverse-primary: '#c00015'
  secondary: '#c8c6c5'
  on-secondary: '#313030'
  secondary-container: '#4a4949'
  on-secondary-container: '#bab8b7'
  tertiary: '#c8c6c5'
  on-tertiary: '#303030'
  tertiary-container: '#929090'
  on-tertiary-container: '#2a2a2a'
  error: '#ffb4ab'
  on-error: '#690005'
  error-container: '#93000a'
  on-error-container: '#ffdad6'
  primary-fixed: '#ffdad6'
  primary-fixed-dim: '#ffb4ac'
  on-primary-fixed: '#410002'
  on-primary-fixed-variant: '#93000e'
  secondary-fixed: '#e5e2e1'
  secondary-fixed-dim: '#c8c6c5'
  on-secondary-fixed: '#1c1b1b'
  on-secondary-fixed-variant: '#474646'
  tertiary-fixed: '#e4e2e1'
  tertiary-fixed-dim: '#c8c6c5'
  on-tertiary-fixed: '#1b1c1c'
  on-tertiary-fixed-variant: '#474746'
  background: '#121414'
  on-background: '#e2e2e2'
  surface-variant: '#333535'
typography:
  display:
    fontFamily: Inter
    fontSize: 48px
    fontWeight: '800'
    lineHeight: 56px
    letterSpacing: -0.02em
  headline-lg:
    fontFamily: Inter
    fontSize: 32px
    fontWeight: '700'
    lineHeight: 40px
    letterSpacing: -0.01em
  headline-lg-mobile:
    fontFamily: Inter
    fontSize: 24px
    fontWeight: '700'
    lineHeight: 32px
  body-md:
    fontFamily: Inter
    fontSize: 16px
    fontWeight: '400'
    lineHeight: 24px
  label-sm:
    fontFamily: JetBrains Mono
    fontSize: 12px
    fontWeight: '500'
    lineHeight: 16px
    letterSpacing: 0.05em
rounded:
  sm: 0.125rem
  DEFAULT: 0.25rem
  md: 0.375rem
  lg: 0.5rem
  xl: 0.75rem
  full: 9999px
spacing:
  unit: 4px
  gutter: 24px
  margin-mobile: 16px
  margin-desktop: 48px
  max-width: 1280px
---

## Brand & Style

The design system is engineered for a high-performance, premium atmosphere, drawing inspiration from automotive precision and elite hardware interfaces. It targets a sophisticated audience that values speed, accuracy, and technical excellence.

The aesthetic follows a **High-Contrast / Bold** direction with a **Minimalist** structural foundation. Visual weight is concentrated on the primary brand color to drive action, while the rest of the interface recedes into a deep, obsidian-like environment. The emotional response is one of authority, luxury, and "dark-mode-first" efficiency.

## Colors

The palette is optimized for OLED displays and high-performance readability. 

- **Primary (#E82127):** A high-energy, vibrant red used exclusively for critical calls to action, active states, and brand-defining accents.
- **Surface & Backgrounds:** Utilizes a tiered charcoal system. The base background is true black (#000000) for maximum contrast, while surfaces like cards and navigation bars use Deep Charcoal (#121212) and elevated layers use Gunmetal (#2A2A2A).
- **Foreground:** Pure white (#FFFFFF) is reserved for primary headers, while secondary text uses a 70% opacity white to maintain hierarchy and reduce eye strain.
- **Status:** Success is indicated by vibrant emerald, and warnings by amber, though these are used sparingly to avoid competing with the Primary Red.

## Typography

This design system utilizes **Inter** for all primary communication, selected for its technical precision and exceptional legibility in dark environments. 

- **Headlines:** Set with tight tracking and heavy weights (Bold/ExtraBold) to convey strength and urgency.
- **Body:** Standardized with ample line height to ensure readability against the high-contrast dark background.
- **Technical Labels:** **JetBrains Mono** is introduced for metadata, values, and status labels to reinforce the "engineered" aesthetic. These should always be set in uppercase with slight tracking to improve scannability.

## Layout & Spacing

The layout follows a **Fixed Grid** model on desktop and a **Fluid Grid** on mobile devices. 

- **Grid:** A 12-column grid system is used for desktop (1280px max-width).
- **Rhythm:** Spacing follows a strict 4px base unit. Component internal padding should favor generous horizontal space to maintain a wide, premium feel.
- **Responsiveness:** On mobile, margins shrink to 16px. Elements that are side-by-side on desktop (like cards) should stack vertically on mobile to maintain the prominence of the imagery and data.

## Elevation & Depth

Elevation in this design system is achieved through **Tonal Layers** rather than heavy shadows. 

- **Level 0 (Background):** Pure black (#000000).
- **Level 1 (Cards/Sections):** Deep Charcoal (#121212) with a 1px solid border of #2A2A2A.
- **Level 2 (Popovers/Modals):** Gunmetal (#2A2A2A) with a soft 10% white inner glow on the top edge to simulate a subtle light source.
- **Shadows:** Only used for floating elements (modals/tooltips). Shadows are 100% black with 0% spread and high blur to create a "void" effect behind the element.

## Shapes

The shape language is "Soft" (0.25rem - 0.75rem). This avoids the playfulness of fully rounded pills while moving away from the aggression of sharp 90-degree corners. 

- **Standard Buttons/Inputs:** 4px (0.25rem) corner radius.
- **Cards/Containers:** 8px (0.5rem) corner radius.
- **Micro-elements (Checkboxes):** 2px corner radius.

This subtle rounding maintains a technical, machine-finished look similar to CNC-milled aluminum.

## Components

- **Buttons:** 
  - *Primary:* Solid Tesla Red (#E82127) with White text. No gradients.
  - *Secondary:* Transparent background with a 1px White or Primary border.
  - *Interaction:* On hover, Primary Red shifts to a slightly darker shade; Secondary buttons gain a 10% white fill.
- **Inputs:** Dark backgrounds (#121212) with a 1px border (#2A2A2A). Focus state triggers a 1px Primary Red border. Use JetBrains Mono for input text to emphasize data entry.
- **Cards:** Use #121212 background with a subtle 1px #2A2A2A border. Titles are Bold Inter; metadata is JetBrains Mono.
- **Chips/Status Tags:** Always utilize a dark fill with high-contrast text. Status indicators (dots) use the Primary Red for "Live" or "Active" states.
- **Data Visualizations:** Charts should use Primary Red as the leading data line, with secondary data sets in varying shades of grey or desaturated blue-greys to ensure the brand color remains the focal point.
