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

  const flow = $(".actor-flow");
  const canvas = $(".flow-canvas");
  const flowStates = {
    plan: {
      phase: "01 / DELEGATION",
      active: ["user", "orchestrator"],
      edges: ["request", "engine", "touch"],
      statuses: [
        "Request sent",
        "Defining tasks",
        "Physics + tests",
        "Input + tests",
        "Observing",
      ],
      description:
        "Your request reaches the orchestrator, which delegates a game-engine task and a touch-controls task to separate workers. The narrator observes.",
    },
    build: {
      phase: "02 / PARALLEL EXECUTION",
      active: ["engine", "touch"],
      edges: ["engine", "touch", "report"],
      statuses: [
        "Following progress",
        "Coordinating",
        "Writing + testing",
        "Writing + testing",
        "Sharing progress",
      ],
      description:
        "Two workers build and test in parallel with separate contexts. Their code and task reports return to the orchestrator. The narrator sends progress updates to you.",
    },
    verify: {
      phase: "03 / VERIFICATION",
      active: ["orchestrator"],
      edges: ["engine", "touch"],
      statuses: [
        "Following progress",
        "Checking the result",
        "Report returned",
        "Report returned",
        "Observing",
      ],
      description:
        "Reports return from both workers to the orchestrator, which inspects the files, runs tests, and verifies the assembled game. Missing work becomes a follow-up task.",
    },
    report: {
      phase: "04 / REPORTING",
      active: ["narrator", "user"],
      edges: ["verified", "report"],
      statuses: [
        "Result received",
        "Result verified",
        "Complete",
        "Complete",
        "Reporting the result",
      ],
      description:
        "The verified outcome reaches the narrator through the event log. The narrator delivers the final report to you, including changes, checks, and remaining limitations.",
    },
  };
  function renderFlow(stage) {
    const state = flowStates[stage];
    flow.dataset.stage = stage;
    $("#flow-phase").textContent = state.phase;
    $(".flow-return-label", flow).textContent =
      stage === "build" ? "Progress updates" : "Final report";
    canvas.setAttribute("aria-label", state.description);
    $$("[data-node]", flow).forEach((node, index) => {
      node.dataset.active = String(state.active.includes(node.dataset.node));
      $("[data-node-status]", node).textContent = state.statuses[index];
    });
    $$("[data-edge]", flow).forEach((edge) => {
      edge.dataset.active = String(state.edges.includes(edge.dataset.edge));
      const returning =
        ["build", "verify"].includes(stage) &&
        ["engine", "touch"].includes(edge.dataset.edge);
      edge.setAttribute(
        "marker-start",
        returning ? "url(#flow-arrow)" : "none",
      );
      edge.setAttribute("marker-end", returning ? "none" : "url(#flow-arrow)");
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
    const { user, orchestrator: orch, engine, touch, narrator } = bounds;
    const mobile = w <= 540;
    const branch = (orch.right + engine.left) / 2;
    const workerBranchY =
      (narrator.bottom + Math.min(engine.top, touch.top)) / 2;
    const paths = {
      request: `M${user.right + 3} ${user.y}H${orch.left - 4}`,
      engine: mobile
        ? `M${orch.right + 3} ${orch.y}H${w - 10}V${workerBranchY}H${engine.x}V${engine.top - 4}`
        : `M${orch.right + 3} ${orch.y}H${branch}V${engine.y}H${engine.left - 4}`,
      touch: mobile
        ? `M${orch.right + 3} ${orch.y}H${w - 10}V${workerBranchY}H${touch.x}V${touch.top - 4}`
        : `M${orch.right + 3} ${orch.y}H${branch}V${touch.y}H${touch.left - 4}`,
      verified: `M${orch.x} ${orch.bottom + 3}V${narrator.y}H${narrator.right + 4}`,
      report: `M${narrator.left - 3} ${narrator.y}H${user.x}V${user.bottom + 4}`,
    };
    $(".flow-edges", flow).setAttribute("viewBox", `0 0 ${w} ${h}`);
    Object.entries(paths).forEach(([name, d]) =>
      $("[data-edge='" + name + "']", flow).setAttribute("d", d),
    );
    for (const [selector, node] of [
      [".flow-return-label", user],
      [".flow-verified-label", orch],
    ]) {
      const label = $(selector, flow);
      label.style.left = node.x + "px";
      label.style.top = (node.bottom + narrator.top) / 2 + "px";
    }
  }
  renderFlow("plan");
  layoutFlow();
  new ResizeObserver(layoutFlow).observe(canvas);
  document.fonts.ready.then(layoutFlow);

  const planContent = document.createElement("template");
  planContent.innerHTML = $("#step-content").innerHTML;
  tabs(".step-tabs", (button) => {
    const stage = button.dataset.step;
    const content =
      stage === "plan" ? planContent : $("#step-" + stage + "-content");
    $("#step-content").replaceChildren(content.content.cloneNode(true));
    renderFlow(stage);
    layoutFlow();
  });

  // Generated at build time from internal/config; no separate model catalog.
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
  $("#preset-select").addEventListener("change", (event) => {
    const preset = presets.get(event.target.value);
    preset.routes.forEach((route) => {
      $("#model-" + route.actor).textContent = route.label;
      $("#provider-" + route.actor).textContent = route.provider;
      $("#effort-" + route.actor).textContent = effortLabel(route.effort);
      const fallback = $("#fallback-" + route.actor);
      fallback.textContent = fallbackLabel(route);
      fallback.hidden = !route.fallback;
    });
    $("#preset-note").textContent = preset.description;
    $("#preset-command").textContent =
      "eagent" +
      (preset.name ? " --preset " + preset.name : "") +
      ' "Build something good"';
  });

  const views = {
    tasks: {
      src: "assets/web-tasks.png",
      alt: "eagent’s Tasks view: per-actor costs, a task timeline with context rollover boundaries, prompt sizes, and completed dossier tasks.",
    },
    chat: {
      src: "assets/web-chat.png",
      alt: "eagent’s Chat view: the narrator’s conversation, an answered question with options, activity, and a message composer.",
    },
  };
  tabs(".workspace-tabs", (button) => {
    const view = views[button.dataset.view];
    $("#workspace-image").src = view.src;
    $("#workspace-image").alt = view.alt;
  });
  const imageDialog = $("#image-dialog");
  $("#expand-screenshot").addEventListener("click", () => {
    $("#dialog-image").src = $("#workspace-image").src;
    $("#dialog-image").alt = $("#workspace-image").alt;
    imageDialog.showModal();
    document.body.classList.add("dialog-open");
  });
  $(".dialog-close").addEventListener("click", () => imageDialog.close());
  imageDialog.addEventListener("close", () =>
    document.body.classList.remove("dialog-open"),
  );
  imageDialog.addEventListener("click", (event) => {
    if (event.target !== imageDialog) return;
    const bounds = imageDialog.getBoundingClientRect();
    if (
      event.clientX < bounds.left ||
      event.clientX > bounds.right ||
      event.clientY < bounds.top ||
      event.clientY > bounds.bottom
    )
      imageDialog.close();
  });

  // Playable Breakout-grid gallery. Game metadata ships in #game-data (built
  // from docs/grid/results.json); iframes load lazily on Play only, and the
  // comparison table plus full-page links work with no JavaScript at all.
  const gameData = $("#game-data")
    ? JSON.parse($("#game-data").textContent)
    : null;
  const gameDialog = $("#game-dialog");
  if (gameData && gameDialog) {
    const gameFrame = $("#game-dialog-frame");
    const gameShot = $("#game-dialog-shot");
    const gameTitle = $("#game-dialog-title");
    const gameMeta = $("#game-dialog-meta");
    const gameOpen = $("#game-dialog-open");
    const shotDesktop = $("#game-shot-desktop");
    const shotMobile = $("#game-shot-mobile");
    let currentGame = null;
    let currentShot = "desktop";
    function renderShot() {
      const shots = {
        desktop: [gameData[currentGame].desktop, 1280, 800, "desktop"],
        mobile: [gameData[currentGame].mobile, 390, 844, "mobile"],
      };
      const [src, width, height, label] = shots[currentShot];
      // Refresh through the staged thumbnail path so a cached desktop still
      // never lingers after switching views.
      gameShot.removeAttribute("src");
      gameShot.src = src;
      gameShot.width = width;
      gameShot.height = height;
      gameShot.alt =
        "Screenshot of " + gameData[currentGame].title + " on " + label;
      shotDesktop.setAttribute(
        "aria-pressed",
        String(currentShot === "desktop"),
      );
      shotMobile.setAttribute("aria-pressed", String(currentShot === "mobile"));
    }
    function openGame(preset) {
      if (!gameData[preset]) return;
      currentGame = preset;
      currentShot = "desktop";
      const game = gameData[preset];
      gameTitle.textContent = game.title;
      gameMeta.textContent =
        preset +
        " · " +
        game.models +
        " · " +
        game.cost +
        " · " +
        game.rubric +
        " rubric · " +
        game.status +
        (game.flag ? " · " + game.flag : "");
      gameOpen.href = game.href;
      gameFrame.title = "Play " + game.title + " (" + preset + ")";
      // Lazy: the game boots only when its dialog opens, never 14 at once.
      gameFrame.src = game.href;
      renderShot();
      gameDialog.showModal();
      document.body.classList.add("dialog-open");
    }
    function closeGame() {
      gameDialog.close();
    }
    $$("[data-play]").forEach((button) => {
      button.addEventListener("click", () => openGame(button.dataset.play));
    });
    shotDesktop.addEventListener("click", () => {
      currentShot = "desktop";
      renderShot();
    });
    shotMobile.addEventListener("click", () => {
      currentShot = "mobile";
      renderShot();
    });
    $("#game-dialog-close").addEventListener("click", closeGame);
    gameDialog.addEventListener("close", () => {
      document.body.classList.remove("dialog-open");
      // Stop the game when the dialog closes so audio and loops end.
      gameFrame.removeAttribute("src");
      currentGame = null;
    });
    gameDialog.addEventListener("click", (event) => {
      if (event.target !== gameDialog) return;
      const bounds = gameDialog.getBoundingClientRect();
      if (
        event.clientX < bounds.left ||
        event.clientX > bounds.right ||
        event.clientY < bounds.top ||
        event.clientY > bounds.bottom
      )
        closeGame();
    });
  }

  tabs(".install-tabs", (button) => {
    const source = button.dataset.install === "source";
    $("#install-command").textContent = source
      ? "git clone https://github.com/ericflo/eagent.git\ncd eagent\ngo build -o eagent ./cmd/eagent"
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
