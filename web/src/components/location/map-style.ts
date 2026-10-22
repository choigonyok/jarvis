import type { ExpressionSpecification, LayerSpecification, StyleSpecification } from "maplibre-gl";

/**
 * The 위치 screen's base map: the console's one grey ramp laid over
 * OpenMapTiles (OpenFreeMap's vector tiles). The map is the dark under the
 * day's light - land is the page's own background, water is a step darker,
 * roads a few steps lighter by how big they are, the names a quiet grey. No
 * hue, no icons: the only bright thing on it is where the operator went.
 *
 * The colors are the tokens in globals.css (oklch, hue 265) worked out to hex:
 * the tile renderer does not read oklch.
 */
const C = {
  land: "#0c0d0f", // --background
  water: "#040508", // l 0.118
  park: "#0f1011", // l 0.171
  building: "#131416", // l 0.19
  minor: "#1a1b1d", // l 0.222
  mid: "#212224", // l 0.25
  major: "#292a2c", // l 0.285
  motorway: "#323335", // l 0.32
  rail: "#1d1e20", // l 0.235
  labelBig: "#7f8083", // l 0.6
  label: "#67696c", // l 0.52
  labelSmall: "#545558", // l 0.45
  waterLabel: "#45484d", // l 0.4
};

/** Korean first: a name in Korean where the map has one, the local name elsewhere. */
const NAME: ExpressionSpecification = ["coalesce", ["get", "name:ko"], ["get", "name"]];

const width = (stops: [number, number][]): ExpressionSpecification => [
  "interpolate",
  ["exponential", 1.5],
  ["zoom"],
  ...stops.flat(),
];

const roads = (id: string, classes: string[], color: string, stops: [number, number][], minzoom = 0): LayerSpecification => ({
  id,
  type: "line",
  source: "omt",
  "source-layer": "transportation",
  minzoom,
  filter: ["all", ["match", ["get", "class"], classes, true, false], ["!=", ["get", "brunnel"], "tunnel"]],
  layout: { "line-cap": "round", "line-join": "round" },
  paint: { "line-color": color, "line-width": width(stops) },
});

/**
 * tileset: the TileJSON URL (/api/map/planet). font: the CSS families to set
 * the names in, regular and medium - the console's own face, so the map's
 * words read like the rest of the page (the glyphs are drawn here in the
 * browser, not fetched).
 */
export function baseStyle(tileset: string, font: { regular: string[]; medium: string[] }): StyleSpecification {
  const halo = { "text-halo-color": C.land, "text-halo-width": 1.4, "text-halo-blur": 0.4 };
  return {
    version: 8,
    sources: { omt: { type: "vector", url: tileset } },
    layers: [
      { id: "land", type: "background", paint: { "background-color": C.land } },
      {
        id: "green",
        type: "fill",
        source: "omt",
        "source-layer": "landcover",
        filter: ["match", ["get", "class"], ["wood", "grass"], true, false],
        paint: { "fill-color": C.park },
      },
      { id: "park", type: "fill", source: "omt", "source-layer": "park", paint: { "fill-color": C.park } },
      { id: "water", type: "fill", source: "omt", "source-layer": "water", paint: { "fill-color": C.water } },
      {
        id: "waterway",
        type: "line",
        source: "omt",
        "source-layer": "waterway",
        paint: { "line-color": C.water, "line-width": width([[10, 0.5], [16, 3]]) },
      },
      {
        id: "building",
        type: "fill",
        source: "omt",
        "source-layer": "building",
        minzoom: 14,
        paint: { "fill-color": C.building, "fill-opacity": ["interpolate", ["linear"], ["zoom"], 14, 0, 15.5, 1] },
      },
      roads("path", ["path", "track"], C.minor, [[15, 0.5], [18, 1.6]], 15),
      roads("minor", ["minor", "service"], C.minor, [[12, 0.3], [15, 1.4], [18, 7]], 12),
      roads("mid", ["tertiary", "secondary"], C.mid, [[9, 0.4], [13, 1.2], [16, 4], [18, 10]]),
      roads("major", ["primary", "trunk"], C.major, [[7, 0.4], [11, 1.1], [15, 3.6], [18, 13]]),
      roads("motorway", ["motorway"], C.motorway, [[6, 0.5], [10, 1.2], [14, 2.8], [18, 15]]),
      {
        id: "rail",
        type: "line",
        source: "omt",
        "source-layer": "transportation",
        minzoom: 11,
        filter: ["all", ["match", ["get", "class"], ["rail", "transit"], true, false], ["!=", ["get", "brunnel"], "tunnel"]],
        paint: { "line-color": C.rail, "line-width": width([[11, 0.5], [16, 1.4]]), "line-dasharray": [3, 2] },
      },
      ...(["Point", "LineString"] as const).map(
        (geometry): LayerSpecification => ({
          id: `water-name-${geometry}`,
          type: "symbol",
          source: "omt",
          "source-layer": "water_name",
          filter: ["==", ["geometry-type"], geometry],
          layout: {
            "text-field": NAME,
            "text-font": font.regular,
            "text-size": 11,
            "symbol-placement": geometry === "LineString" ? "line" : "point",
          },
          paint: { "text-color": C.waterLabel, ...halo },
        }),
      ),
      {
        id: "road-name",
        type: "symbol",
        source: "omt",
        "source-layer": "transportation_name",
        minzoom: 15,
        filter: ["match", ["get", "class"], ["motorway", "trunk", "primary", "secondary", "tertiary", "minor"], true, false],
        layout: {
          "text-field": NAME,
          "text-font": font.regular,
          "text-size": 10.5,
          "symbol-placement": "line",
          "text-max-angle": 30,
        },
        paint: { "text-color": C.labelSmall, ...halo },
      },
      {
        id: "station",
        type: "symbol",
        source: "omt",
        "source-layer": "poi",
        minzoom: 14,
        filter: ["all", ["==", ["get", "class"], "railway"], ["match", ["get", "subclass"], ["station", "subway"], true, false]],
        layout: { "text-field": NAME, "text-font": font.regular, "text-size": 10.5 },
        paint: { "text-color": C.labelSmall, ...halo },
      },
      {
        id: "neighbourhood",
        type: "symbol",
        source: "omt",
        "source-layer": "place",
        minzoom: 13,
        filter: ["match", ["get", "class"], ["neighbourhood", "quarter"], true, false],
        layout: { "text-field": NAME, "text-font": font.regular, "text-size": 11, "text-max-width": 7 },
        paint: { "text-color": C.labelSmall, ...halo },
      },
      {
        id: "suburb",
        type: "symbol",
        source: "omt",
        "source-layer": "place",
        minzoom: 10,
        filter: ["match", ["get", "class"], ["suburb", "village"], true, false],
        layout: { "text-field": NAME, "text-font": font.regular, "text-size": 12, "text-max-width": 7 },
        paint: { "text-color": C.label, ...halo },
      },
      {
        id: "city",
        type: "symbol",
        source: "omt",
        "source-layer": "place",
        filter: ["match", ["get", "class"], ["city", "town"], true, false],
        layout: {
          "text-field": NAME,
          "text-font": font.medium,
          "text-size": ["interpolate", ["linear"], ["zoom"], 6, 11, 12, 15],
          "text-max-width": 8,
        },
        paint: { "text-color": C.labelBig, ...halo },
      },
    ],
  };
}
