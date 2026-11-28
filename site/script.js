/* The page is static. Interactions illustrate eagent; none start agents or call APIs. */
(() => {
  "use strict";
  const $ = (selector, scope = document) => scope.querySelector(selector);
  const $$ = (selector, scope = document) => [
    ...scope.querySelectorAll(selector),
  ];

  const menuButton = $(".menu-toggle");
  const navigation = $("#navigation");
  function closeMenu() {
    menuButton.setAttribute("aria-expanded", "false");
    menuButton.setAttribute("aria-label", "Open navigation");
    navigation.classList.remove("open");
  }
  menuButton.addEventListener("click", () => {
    const open = menuButton.getAttribute("aria-expanded") !== "true";
    menuButton.setAttribute("aria-expanded", String(open));
    menuButton.setAttribute(
      "aria-label",
      open ? "Close navigation" : "Open navigation",
    );
    navigation.classList.toggle("open", open);
  });
  navigation.addEventListener("click", (event) => {
    if (event.target.closest("a")) closeMenu();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && navigation.classList.contains("open")) {
      closeMenu();
      menuButton.focus();
    }
  });
  document.addEventListener("click", (event) => {
    if (!event.target.closest(".site-header")) closeMenu();
  });
  matchMedia("(min-width: 651px)").addEventListener("change", (event) => {
    if (event.matches) closeMenu();
  });

  // Shared tabs with automatic activation, arrow keys, Home, and End.
  function tabs(selector, render) {
    const group = $(selector);
    const buttons = $$('[role="tab"]', group);
    function activate(button) {
      buttons.forEach((item) => {
        const selected = item === button;
        item.setAttribute("aria-selected", String(selected));
        item.tabIndex = selected ? 0 : -1;
      });
      const panel = document.getElementById(
        button.getAttribute("aria-controls"),
      );
      panel.setAttribute("aria-labelledby", button.id);
      render(button);
    }
    buttons.forEach((button, index) => {
      button.addEventListener("click", () => activate(button));
      button.addEventListener("keydown", (event) => {
        let next = index;
        if (event.key === "ArrowRight" || event.key === "ArrowDown")
          next = (index + 1) % buttons.length;
        else if (event.key === "ArrowLeft" || event.key === "ArrowUp")
          next = (index - 1 + buttons.length) % buttons.length;
        else if (event.key === "Home") next = 0;
        else if (event.key === "End") next = buttons.length - 1;
        else return;
        event.preventDefault();
        buttons[next].focus();
        activate(buttons[next]);
      });
    });
  }

  let toastTimer;
  function announce(text) {
    const toast = $("#toast");
    clearTimeout(toastTimer);
    toast.textContent = text;
    toast.classList.add("visible");
    toastTimer = setTimeout(() => toast.classList.remove("visible"), 3200);
  }
  async function copy(text) {
    try {
      if (!navigator.clipboard?.writeText)
        throw new Error("Clipboard API unavailable");
      await navigator.clipboard.writeText(text);
    } catch {
      const active = document.activeElement;
      const input = document.createElement("textarea");
      input.value = text;
      input.setAttribute("aria-label", "Command to copy");
      Object.assign(input.style, {
        position: "fixed",
        left: "-9999px",
        top: "0",
      });
      document.body.append(input);
      input.select();
      let copied = false;
      try {
        copied = document.execCommand("copy");
      } finally {
        input.remove();
        active?.focus({ preventScroll: true });
      }
      if (!copied) throw new Error("Clipboard access unavailable");
    }
  }
  const copyTimers = new WeakMap();
  $$("[data-copy]").forEach((button) => {
    button.addEventListener("click", async () => {
      const source = document.getElementById(button.dataset.copy);
      try {
        await copy(source.textContent);
        clearTimeout(copyTimers.get(button));
        button.classList.add("is-copied");
        $("use", button).setAttribute("href", "#i-check");
        announce("Copied. Your terminal is next.");
        copyTimers.set(
          button,
          setTimeout(() => {
            button.classList.remove("is-copied");
            $("use", button).setAttribute("href", "#i-copy");
          }, 2400),
        );
      } catch {
        const range = document.createRange();
        range.selectNodeContents(source);
        const selection = getSelection();
        selection.removeAllRanges();
        selection.addRange(range);
        announce(
          "Clipboard unavailable. The command is selected for manual copying.",
        );
      }
    });
  });

  // One turn, step by step. Nodes are listed in document order: user,
  // eagent, agent. Edges: "words" (you → agent, before any model call),
  // "relay" (between eagent and the agent), "tell" (eagent → you).
  const flow = $(".actor-flow");
  const canvas = $(".flow-canvas");
  const flowStates = {
    send: {
      phase: "01 / YOUR WORDS",
      active: ["user", "agent"],
      edges: ["words"],
      relay: "in",
      labels: { words: "Word for word" },
      statuses: ["Message sent", "No model call", "Typed in as written"],
      description:
        "Your message goes straight to Codex's terminal, word for word. eagent types it in the moment it arrives, before any model call.",
    },
    watch: {
      phase: "02 / WATCHING",
      active: ["eagent", "agent"],
      edges: ["relay"],
      relay: "in",
      labels: { relay: "New steps" },
      statuses: ["Away", "Reading new steps", "Working"],
      description:
        "Codex works. eagent reads the new steps from Codex's own transcript. When there is nothing new, no model call is made.",
    },
    progress: {
      phase: "03 / PROGRESS",
      active: ["eagent", "user"],
      edges: ["relay", "tell"],
      relay: "in",
      labels: { relay: "New steps", tell: "A line or two" },
      statuses: ["Gets an update", "Says what changed", "Still working"],
      description:
        "From the new steps, eagent tells you in a sentence or two what Codex has done, with the files, commands and numbers that matter.",
    },
    dialog: {
      phase: "04 / A STUCK DIALOG",
      active: ["eagent", "agent"],
      edges: ["relay"],
      relay: "both",
      labels: { relay: "Screen in, keys out" },
      statuses: ["Not disturbed", "Reads screen, sends keys", "Dialog cleared"],
      description:
        "Codex is stuck on a dialog. eagent reads its screen, answers with keys, and reads the screen again to check that it took. You are not asked unless the choice is yours.",
    },
    end: {
      phase: "05 / TURN ENDS",
      active: ["user", "eagent", "agent"],
      edges: ["relay", "tell"],
      relay: "in",
      labels: { relay: "Turn report", tell: "Short version + report" },
      statuses: ["Reads both", "Writes the short version", "Turn finished"],
      description:
        "Codex finishes its turn. Its full report reaches you as a message of its own, and eagent adds the short version: what changed, what comes next, and anything that needs you.",
    },
    ask: {
      phase: "06 / YOUR DECISION",
      active: ["user", "eagent"],
      edges: ["tell", "relay"],
      relay: "out",
      labels: { tell: "Question + options", relay: "Your answer" },
      statuses: [
        "Picks an option",
        "Asks, then passes it on",
        "Waiting on you",
      ],
      description:
        "Codex asks something only you can decide. eagent asks you in plain words with the choices as options, and your answer goes back to Codex.",
    },
  };
  const arrow = "url(#flow-arrow)";
  function renderFlow(stage) {
    const state = flowStates[stage];
    flow.dataset.stage = stage;
    $("#flow-phase").textContent = state.phase;
    canvas.setAttribute("aria-label", state.description);
    $$("[data-node]", flow).forEach((node, index) => {
      node.dataset.active = String(state.active.includes(node.dataset.node));
      $("[data-node-status]", node).textContent = state.statuses[index];
    });
    $$("[data-edge]", flow).forEach((edge) => {
      edge.dataset.active = String(state.edges.includes(edge.dataset.edge));
    });
    // The relay path is drawn from eagent to the agent: its start points at
    // eagent (what eagent reads), its end at the agent (what eagent sends).
    const relay = $("[data-edge='relay']", flow);
    relay.setAttribute("marker-start", state.relay === "out" ? "none" : arrow);
    relay.setAttribute("marker-end", state.relay === "in" ? "none" : arrow);
    $$("[data-label]", flow).forEach((label) => {
      label.textContent = state.labels[label.dataset.label] || "";
      label.hidden = !state.labels[label.dataset.label];
    });
  }
  // Route connectors between actual card bounds so labels stay full-size on
  // narrow screens. The SVG supplies a static first frame before enhancement.
  function layoutFlow() {
    const frame = canvas.getBoundingClientRect();
    const w = frame.width,
      h = frame.height;
    const bounds = {};
    $$("[data-node]", flow).forEach((node) => {
      const rect = node.getBoundingClientRect();
      bounds[node.dataset.node] = {
        left: rect.left - frame.left,
        right: rect.right - frame.left,
        top: rect.top - frame.top,
        bottom: rect.bottom - frame.top,
        x: rect.left - frame.left + rect.width / 2,
        y: rect.top - frame.top + rect.height / 2,
      };
    });
    const { user, eagent, agent } = bounds;
    const paths = {
      words: `M${user.right + 3} ${user.y}H${agent.left - 4}`,
      relay: `M${eagent.right + 4} ${eagent.y}H${agent.x}V${agent.bottom + 4}`,
      tell: `M${eagent.left - 3} ${eagent.y}H${user.x}V${user.bottom + 4}`,
    };
    $(".flow-edges", flow).setAttribute("viewBox", `0 0 ${w} ${h}`);
    Object.entries(paths).forEach(([name, d]) =>
      $("[data-edge='" + name + "']", flow).setAttribute("d", d),
    );
    const wide = agent.left - user.right > 150;
    const middle = (user.bottom + eagent.y) / 2;
    const places = {
      words: wide
        ? [(user.right + agent.left) / 2, user.y]
        : [w / 2, user.bottom + 16],
      relay: [agent.x, (agent.bottom + eagent.y) / 2],
      tell: [user.x, middle],
    };
    Object.entries(places).forEach(([name, [x, y]]) => {
      const label = $("[data-label='" + name + "']", flow);
      label.style.left = x + "px";
      label.style.top = y + "px";
    });
  }
  renderFlow("send");
  layoutFlow();
  new ResizeObserver(layoutFlow).observe(canvas);
  document.fonts.ready.then(layoutFlow);

  const firstContent = document.createElement("template");
  firstContent.innerHTML = $("#step-content").innerHTML;
  tabs(".step-tabs", (button) => {
    const stage = button.dataset.step;
    const content =
      stage === "send" ? firstContent : $("#step-" + stage + "-content");
    $("#step-content").replaceChildren(content.content.cloneNode(true));
    renderFlow(stage);
    layoutFlow();
  });

  // Generated at build time from internal/config; no separate model catalog.
  // Each preset names one model for eagent's loop (the "eagent" route).
  const catalog = JSON.parse($("#preset-data").textContent);
  const presets = new Map(
    catalog.presets.map((preset) => [preset.name, preset]),
  );
  const effortLabel = (effort) =>
    effort === "none"
      ? "no reasoning"
      : effort
        ? effort + " effort"
        : "provider default";
  function fallbackLabel(route) {
    const labels = [];
    for (
      let fallback = route.fallback;
      fallback;
      fallback = fallback.fallback
    ) {
      labels.push(
        fallback.label +
          " · " +
          fallback.provider +
          " · " +
          effortLabel(fallback.effort),
      );
    }
    return labels.length ? "Fallback: " + labels.join(" → ") : "";
  }
  const presetFlag = (preset) =>
    !preset.name || preset.name === catalog.default
      ? ""
      : " --preset " + preset.name;
  const examplePrompt = ' "add a Makefile with build and test targets"';
  $("#preset-select").addEventListener("change", (event) => {
    const preset = presets.get(event.target.value);
    const route = preset.routes.find((item) => item.actor === "eagent");
    $("#model-eagent").textContent = route.label;
    $("#provider-eagent").textContent = route.provider;
    $("#effort-eagent").textContent = effortLabel(route.effort);
    const fallback = $("#fallback-eagent");
    fallback.textContent = fallbackLabel(route);
    fallback.hidden = !route.fallback;
    $("#preset-note").textContent = preset.description;
    $("#preset-command").textContent =
      "eagent" + presetFlag(preset) + examplePrompt;
  });

  tabs(".install-tabs", (button) => {
    const source = button.dataset.install === "source";
    $("#install-command").textContent = source
      ? "git clone https://github.com/ericflo/eagent.git\ncd eagent\nmake build"
      : "go install github.com/ericflo/eagent/cmd/eagent@latest";
    $("#install-note").innerHTML = source
      ? "Put the built binary on your <code>PATH</code>, or run it with <code>./eagent</code>."
      : "Make sure Go’s bin directory is on your <code>PATH</code>.";
  });

  // Pause decorative animation while offscreen or when the document is hidden.
  const trails = $$(".orbit-trail");
  let heroVisible = true;
  function updateAnimation() {
    trails.forEach((trail) => {
      trail.style.animationPlayState =
        document.hidden || !heroVisible ? "paused" : "running";
    });
  }
  if ("IntersectionObserver" in window) {
    new IntersectionObserver((entries) => {
      heroVisible = entries[0].isIntersecting;
      updateAnimation();
    }).observe($(".hero-art"));
  }
  document.addEventListener("visibilitychange", updateAnimation);
  document.documentElement.classList.add("enhanced");
})();
