# $NUTZ — La ardilla que te paga

**Whitepaper v0.2 · Borrador para revisión interna · Septiembre 2026** (v0.1 actualizado el 12-09-2026 para alinearlo con la especificación de ingeniería v0.2)
**Cadena:** Robinhood Chain (L2 de Ethereum, chain id 4663)
**Ticker:** NUTZ · **Sitio:** nutz.wtf · **Redes:** @stacknutz

> Kinda chic to get paid in nutz.

---

## Resumen

$NUTZ es una memecoin en Robinhood Chain que paga a sus holders cada hora en dos cosas: **acciones tokenizadas** y **efectivo (USDG)**. Cada operación con $NUTZ genera una comisión. El 100 % de la parte del creador de esa comisión va al **Nut Vault**, que compra una canasta fija de acciones tokenizadas (SPY, NVDA, MU, SPCX) más USDG y la reparte entre los holders en proporción a cuánto tienen y cuánto tiempo lo han tenido. El 98 % va a los holders; el 2 % paga el gas del propio sistema para que funcione para siempre sin que nadie lo financie. El equipo no se queda con nada. Las reglas viven en contratos de código abierto y cada pago puede verificarse de forma independiente.

La mascota es la Ardilla del Tornado. Aguanta la tormenta y sigue acumulando nutz. Tú también.

---

## 1. Por qué existe

Robinhood Chain se lanzó en julio de 2026 y las memecoins se convirtieron de inmediato en su mayor fuente de actividad. Las ganadoras hasta ahora comparten algo: tratan de **dinero y mercados**, porque quienes las operan son usuarios de Robinhood. Cash Cat es folclore de Robinhood. Artificial Inu está emparejada con acciones de Nvidia. Saylormoon acumula MicroStrategy. La operación más caliente de la cadena son las memecoins que tocan acciones reales.

Casi todas tienen un hueco. *Hablan* de pagar a los holders. Rara vez lo *hacen*, de forma automática, a tu wallet, con un horario al que puedas poner el reloj. Las pocas que sí (MarsCoin en BNB, BOOMER en Robinhood Chain) demostraron la demanda: los holders se quedan, porque irse significa que los pagos se detienen.

$NUTZ cierra ese hueco con una promesa simple: **hold la ardilla, cobra en nutz** — acciones tokenizadas reales y dólares reales, cada hora, sin reclamar nada en la mayoría de los casos, sin mínimo, sin comisión para el equipo.

---

## 2. El meme

**La Ardilla del Tornado.** Una ardilla caricaturesca que levanta tranquilamente la pata mientras un tornado se la lleva. El formato despegó en TikTok en julio de 2026 y para finales de agosto estaba en todo X — la imagen de reacción para "todo se está cayendo y yo estoy bien". El chiste nació como un edit de una escena de película; la ardilla en sí no es personaje de nadie. NUTZ usa su propia ardilla original y nunca el material de la película: la misma sensación, nuestro propio dibujo.

Una ardilla es además la inversionista original: acumula nutz para el invierno y no los toca. La metáfora se escribe sola. Los nutz son acciones. El tornado es el mercado. La ardilla eres tú.

**La voz: Kinda Chic.** El formato "Kinda chic to…" convierte cosas nada glamorosas en presumidas. Es el motor de contenido perfecto para una moneda que paga dividendos, porque cada captura de un pago se vuelve un post:

- Kinda chic to own Nvidia because of a squirrel.
- Kinda chic to have a retirement plan at 23.
- Kinda chic to get a paycheck every hour and do nothing.

Los holders escriben el marketing. La ardilla solo sigue repartiendo.

---

## 3. Cómo funciona (versión corta)

1. **Compras $NUTZ** en Robinhood Chain (primero en la bonding curve, después en un pool de Uniswap v4 con liquidez bloqueada).
2. **Cada operación paga una comisión.** La parte del creador — más un impuesto de creador fijado al máximo del protocolo — se envía al **Nut Vault**, no a una wallet del equipo.
3. **El Nut Vault convierte las comisiones** en el Stash (una canasta fija de acciones tokenizadas) y USDG.
4. **Cada hora, la ardilla suelta nutz.** Las recompensas se asignan a cada holder según su saldo ponderado por tiempo. Las wallets por encima de un umbral pequeño reciben el pago automáticamente; las demás pueden reclamarlo cuando quieran.
5. **Una parte va al Acorn Draw** — un sorteo semanal con un ganador del Golden Acorn más niveles Silver y Rain cuyo número de ganadores crece con la base de holders.

Eso es todo. Sin staking. Sin bloqueos. Sin "roadmap de utilidad". Hold la ardilla, cobra en nutz.

---

## 4. Token

| Concepto | Valor |
|---|---|
| Nombre / Ticker | Nutz / NUTZ |
| Cadena | Robinhood Chain (chain id 4663) |
| Launchpad | Pons v2 |
| Supply | Fijo al lanzamiento según la configuración de Pons; sin función de mint |
| Activo de cotización | ETH (ver Apéndice A sobre por qué no una acción tokenizada) |
| Asignación al equipo | **0**. Todo el supply se emite a la bonding curve. |
| Compra dev | Pequeña (≈0.3–0.5 ETH), pública desde el lanzamiento. Recibe los repartos de Stash y Cash como cualquier holder con un multiplicador fijo de 1.0× (sin bono Winter Mode); excluida permanentemente del Acorn Draw |
| Liquidez | 100 % bloqueada para siempre en un pool de Uniswap v4 al graduarse. No existe función de desbloqueo. |
| Receptor de comisiones del creador | El contrato Converter del Nut Vault, fijado en la creación. El Converter no tiene ninguna función para cambiarlo. El dueño del protocolo Pons conserva un poder, divulgado, para redirigir el receptor de comisiones de cualquier lanzamiento tras un aviso público de 3 días; lo monitoreamos on-chain y lo anunciaríamos de inmediato (ver 8) |
| Impuesto de creador | Máximo del protocolo (`maxCreatorTaxBps` al lanzar), 100 % al Nut Vault |
| Costo operativo | Autofinanciado: una parte Ops del 2 % paga las transacciones del protocolo; el gas del envío automático se descuenta del USDG del receptor. Sin financiamiento del equipo, nunca |
| Buyback | Apagado — cada dólar de comisiones va a los holders, no a un vesting del creador |

**Por qué Pons.** Pons es donde está la atención de Robinhood Chain (~59 % del volumen de launchpads, más de $500M diarios en su pico). Su flujo de curva a pool bloqueado significa que no hay nada que el equipo pueda "ruggear": no hay liquidez que retirar, no hay mint, no hay impuesto que subir después. Lo único que el creador controla tras el lanzamiento es *a dónde van las comisiones* — y eso apunta a un contrato cuyas reglas puedes leer y que no puede apuntarlas a ningún otro sitio.

---

## 5. El Nut Vault

El Nut Vault es el motor de recompensas de $NUTZ: un conjunto de contratos de código abierto en Robinhood Chain (un Converter, un Distributor y un contrato de sorteo) que recibe el 100 % de las comisiones del creador y las devuelve a los holders.

### 5.1 Entradas

- La parte del creador de la comisión estándar de Pons (cobrada en cada compra y venta, en la curva y en el pool).
- El impuesto de creador (fijado al máximo que permite el protocolo).
- Cualquier donación voluntaria. (Los depósitos están abiertos.)

Las comisiones se acumulan en ETH en la curva de bonding. Tras la graduación, parte de las comisiones del pool se acumulan en NUTZ y Pons las convierte a ETH antes de que el vault pueda cobrarlas. El vault reclama cada hora lo que se le haya acreditado.

### 5.2 El reparto

| Parte | Destino | Qué reciben los holders |
|---|---|---|
| **60 %** | **The Stash** | Acciones tokenizadas: SPY, NVDA, MU, SPCX en partes iguales |
| **28 %** | **Cash** | Stablecoin USDG |
| **10 %** | **Acorn Draw** | Sorteo semanal: 1 Golden Acorn + niveles Silver y Rain que escalan |
| **2 %** | **Ops** | Se conserva en ETH para pagar las transacciones del propio sistema (ver 5.6). Con tope; el excedente vuelve a los holders |

**Los dividendos se reinvierten solos.** Los Stock Tokens de Robinhood incluyen un multiplicador por eventos corporativos: cuando SPY paga dividendo o NVDA hace un split, el multiplicador sube y cada token que ya tienes en tu wallet representa más acciones subyacentes. Nada que reclamar, nada que hacer. Los nutz que la ardilla te soltó el mes pasado siguen creciendo por su cuenta.

**Por qué acciones *y* efectivo.** Las acciones tokenizadas son la presunción ("una ardilla me compró Nvidia"). El efectivo es la prueba ("sí llegó a mi wallet"). Juntos responden a la queja que recibe cada meme de dividendos — *¿dónde está el dinero?* — con una captura de pantalla.

**Por qué esta canasta.** SPY es el mercado. NVDA es la IA. MU es memoria — la operación de 2026, y la que mejor encaja con una ardilla que guarda cosas. SPCX es el espacio. Cuatro tickers, cuatro historias, y sigue cabiendo en un tuit. La canasta se fija al lanzamiento y nadie puede cambiarla.

Liquidez on-chain verificada el 9 de septiembre de 2026 (pool más profundo en Robinhood Chain por Stock Token): NVDA $8.4M, SPCX $3.4M, MU $1.8M; SPY es el Stock Token más operado de la cadena. El vault compra directamente en Uniswap (primero pools v3, v4 como respaldo) con un tope de slippage estricto, y los montos son horarios, de modo que las compras nunca mueven un pool de forma relevante. Si el swap de una acción falla, o Robinhood pausa ese token, la parte de esa hora se paga en USDG; nada se queda esperando dentro del vault.

### 5.3 El drop (mecánica de distribución)

- **Las épocas son de una hora.** Al cierre de cada época, el indexador del vault calcula el **saldo promedio ponderado por tiempo (TWAB)** de cada wallet durante esa hora. Comprar en el minuto 59 gana 1/60 de una hora completa. Vender en el minuto 1 gana 1/60. Tener el token unos segundos no gana nada.
- **Las asignaciones son proporcionales al TWAB**, multiplicadas por el bono de racha (5.4).
- **El pago es por reclamo con envío automático.** Cada época publica on-chain una raíz Merkle de las asignaciones. Cualquier holder puede reclamar cuando quiera, pagando su propio gas. Un keeper envía automáticamente las recompensas a cada wallet con saldo pendiente suficiente, descontando el costo real del gas del USDG de esa wallet (ver 5.6), así que la mayoría nunca tiene que hacer clic en nada y el sistema nunca necesita dinero externo.
- **Sin mínimo.** Un solo $NUTZ gana su parte.
- **Excluidos de recompensas:** el pool de Uniswap, el locker y el vault de buyback de Pons, el propio Nut Vault y cualquier dirección de depósito de exchange centralizado que la comunidad señale.
- **La wallet de la compra dev** es una dirección pública con nombre. Recibe los repartos de Stash y Cash con un 1.0× fijo en el código (nunca puede ganar el bono Winter Mode) y está excluida del Acorn Draw también por código. Su saldo y sus recompensas acumuladas se muestran en el dashboard.

Todo lo que hay detrás de un pago es reproducible a partir de datos públicos de la cadena. Las reglas de pago se publican como un **verificador** de código abierto: una herramienta pequeña que recalcula las asignaciones de cualquier hora desde la cadena y confirma que coinciden con la raíz publicada. Cualquiera puede ejecutarla. Si el equipo desaparece, el vault conserva su saldo, los reclamos siguen funcionando y la comunidad puede reconstruir la distribución a partir del verificador público.

### 5.4 Winter Mode (bono de racha)

A las ardillas se les premia por no tocar el stash.

| Días sin vender | Multiplicador |
|---|---|
| 0–6 días | 1.0× |
| 7–29 días | 1.25× |
| 30+ días | 1.5× |

Cualquier transferencia saliente reinicia la racha a cero: vender, enviar a otra wallet o quemar. Aumentar la posición no. Los multiplicadores aplican a los pools de Stash y Cash; el Acorn Draw usa boletos (5.5).

### 5.5 El Acorn Draw (sorteo)

Cada semana, el 10 % de las entradas del vault de esa semana se convierte en el **Acorn pool**, pagado en acciones tokenizadas a holders seleccionados al azar en tres niveles. El número de ganadores crece automáticamente con el número de holders, pero un premio mínimo garantiza que cada premio valga la pena publicar.

| Nivel | Parte del pool | Ganadores | Premio mínimo |
|---|---|---|---|
| **Golden Acorn** | 50 % | Siempre 1 | $2,500 (se acumula si no se alcanza) |
| **Silver Acorns** | 30 % | máx(3, holders ÷ 1,000) | $250 |
| **Acorn Rain** | 20 % | máx(10, holders ÷ 100) | $25 |

- **Premio mínimo:** si el pool no alcanza para la fórmula al mínimo, el número de ganadores de ese nivel se reduce hasta que sí. Los premios nunca se diluyen en migajas.
- **Acumulación:** si el Golden Acorn quedara por debajo de su mínimo, pasa al pool de la semana siguiente. Un acorn que crece es mejor contenido que un ganador pequeño.
- **Boletos, no peso:** un boleto por cada 1,000 $NUTZ de TWAB semanal, con tope equivalente al 1 % del supply por wallet. Winter Mode (30+ días) duplica los boletos de una wallet. Los holders pequeños tienen oportunidad real; las ballenas no pueden farmearlo.
- **Aleatoriedad verificable:** los ganadores se seleccionan con drand, el faro público de aleatoriedad operado por la League of Entropy desde 2019; la lista de boletos se compromete on-chain antes de que exista el valor del faro, la firma del faro se verifica on-chain, y la semilla, la lista de boletos y el resultado son públicos.
- **El sorteo es un evento:** el dashboard muestra una cuenta regresiva y luego las wallets ganadoras; un bot de X publica cada premio.
- **Excluidos:** la wallet de la compra dev y todas las direcciones excluidas de las recompensas regulares.

Ejemplo: 500 holders y un pool de $12k pagan 1 × $6,000, 3 × $1,200 y 10 × $240. Con 50,000 holders y un pool de $300k pagan 1 × $150,000, 50 × $1,800 y 500 × $120 — la escalera se ensancha sola.

Si el token crece mucho, se puede añadir un Acorn Rain diario junto al Golden Acorn semanal. La cadencia es un cambio de configuración, no un rediseño.

### 5.6 Autofinanciamiento (quién paga el gas)

NUTZ debe funcionar sin financiamiento externo, para siempre. Dos mecanismos cubren su único costo, el gas de las transacciones:

- **Parte Ops (2 % de las entradas).** Se conserva en ETH, nunca se convierte. Paga las transacciones que el propio sistema debe enviar: barrido y swaps cada hora, publicación de la raíz cada hora, la solicitud semanal de aleatoriedad y la raíz del sorteo — unas 25–30 transacciones al día sin importar cuántos holders haya. El saldo Ops tiene un tope (inicialmente 0.5 ETH); todo lo que lo supere fluye automáticamente al siguiente drop. El saldo es público en el dashboard.
- **Envío automático pagado por el receptor.** Enviar recompensas a miles de wallets escala con los holders, así que no puede salir de una parte fija. Cuando el keeper envía las recompensas de una wallet, descuenta el costo real del gas de esa transferencia del USDG de la wallet y reembolsa a la wallet Ops (en USDG, que la wallet Ops cambia periódicamente de vuelta a ETH). Una wallet solo recibe el envío cuando el descuento es como máximo el 5 % de su pago; los saldos menores se acumulan hasta calificar. Cada descuento se detalla en el dashboard. Quien prefiera puede reclamar manualmente en cualquier momento y pagar el gas desde su propia wallet.

Efecto neto: los holders reciben el 98 % de todas las comisiones, menos su propio "franqueo" en los envíos. Sin tesorería, sin subsidio del equipo y nada que deje de funcionar si el equipo desaparece.

---

## 6. Ejemplo numérico (solo ilustrativo)

Supuestos: $1,000,000 de volumen diario; la comisión base de Pons de 1.0 % por operación con 70 % llegando al lado del creador tras la parte del protocolo (ambos confirmados on-chain); impuesto de creador asumido en 1.0 % `[el tope del protocolo se lee de la factory al lanzar; si es mayor, la entrada al vault sube en proporción]`.

- Comisión base al lado del creador: $1,000,000 × 1.0 % × 70 % ≈ **$7,000/día**
- Impuesto de creador: $1,000,000 × 1.0 % ≈ **$10,000/día**
- **Entrada al vault ≈ $17,000/día**, de los cuales ≈ $10,200 compran el Stash, ≈ $4,760 son USDG, ≈ $1,700 se acumulan en el pool del Acorn Draw (≈ $12k por semana) y ≈ $340 cubren el gas del propio sistema.

Una wallet con el 0.1 % del supply a 1.0× recibiría unos **$15/día** en acciones tokenizadas y USDG; a 1.5× (racha de 30+ días) unos **$22/día**. Con $10M de volumen diario las cifras son 10 veces mayores. Con $50k diarios son la vigésima parte. El vault paga lo que el mercado opera — nunca paga desde una tesorería, porque no hay tesorería.

---

## 7. Lanzamiento

- **Pre-lanzamiento (T-5 a T-1 días):** sitio, X y Telegram activos; contratos del Nut Vault desplegados y verificados; repositorio del verificador público; dirección del contrato anunciada únicamente vía nutz.wtf y @stacknutz.
- **Lanzamiento:** `launchAndBuy` en una sola transacción (la compra dev del creador no puede ser adelantada). Wallets nombradas del equipo y la comunidad exentas del impuesto anti-sniper inicial para que los primeros minutos no sean una carrera de bots.
- **Curva:** umbral de graduación de 4.2 ETH (confirmado en lanzamientos recientes cotizados en ETH). Las recompensas empiezan desde la primera hora, en la curva — no después de la graduación.
- **Graduación:** liquidez bloqueada permanentemente en Uniswap v4. Nada cambia para los holders.
- **Primer Acorn Draw:** al cierre de la semana de lanzamiento.

El compromiso del equipo: **cero asignación, cero comisión para el equipo, una wallet dev pública que gana exactamente lo que gana cualquier holder (sin bono, sin sorteo), todo público.**

---

## 8. Lo que realmente estás recibiendo (lee esto)

- **Los Stock Tokens de Robinhood Chain no son acciones.** Son tokens emitidos por una entidad de Robinhood que siguen el precio de la acción subyacente. No tienes derechos de voto. Sí tienes exposición al precio y la capacidad de operarlos o moverlos on-chain. Se operan sin permiso en Uniswap: cualquiera con una wallet puede comprarlos, venderlos o conservarlos, sin cuenta de Robinhood. Lo que depende de dónde vivas es si puedes redimirlos *a través de Robinhood* (por ahora solo sus clientes de la UE) y si tus reglas locales te permiten tenerlos. Para la mayoría de los holders, el mercado on-chain es la única salida. **Conoce tus reglas locales.**
- **$NUTZ no tiene valor intrínseco.** Es un meme. Su precio puede ir a cero. Las recompensas son una parte de las comisiones de trading; si nadie opera, nadie cobra.
- **El Nut Vault es código.** Los contratos y el verificador son de código abierto, revisados antes del lanzamiento con herramientas públicas de seguridad y pruebas de invariantes, y cubierto por un bug bounty público desde el primer día. Una auditoría externa es un punto post-lanzamiento, financiado conforme crezca el token, y se publicará al completarse. El código tiene errores; el vault está construido para que un error solo pueda tocar una hora de comisiones, porque barre y distribuye cada hora y nunca guarda una tesorería.
- **Nadie controla tus tokens.** Sin mint, sin congelamiento, sin lista negra, sin liquidez desbloqueable, sin forma de que nadie retire del vault. Las únicas acciones administrativas en todo el sistema requieren firma 2 de 3 y son visibles on-chain: publicar o anular la raíz de pagos de una hora (limitada a lo que esa hora realmente ganó), un interruptor de emergencia que solo puede redirigir la parte de una acción a USDG si Robinhood pausa o bloquea ese token (volver a activarla tiene un timelock de 48 h), añadir direcciones de depósito de exchanges a la lista de exclusión (48 h), rotar firmantes (48 h) y el puntero al contrato del sorteo (48 h). Ninguna puede mover valor lejos de los holders. Hay una cosa fuera de nuestro control y debes saberla: Pons, el launchpad, conserva un poder a nivel de protocolo para redirigir las comisiones de creador de cualquier lanzamiento a una nueva dirección tras un aviso público de 3 días. Nuestro Converter no tiene ninguna función para cambiar el receptor por sí mismo, vigilamos ese aviso on-chain y lo anunciaríamos en el momento en que apareciera. Las comisiones ya acreditadas al vault no se pueden recuperar.
- **No es asesoría financiera. Sin afiliación con Robinhood, Pons ni ninguna empresa del Stash.** La ardilla es un meme. Por favor, no demanden a la ardilla.

---

## 9. Roadmap (corto a propósito)

1. **Lanzamiento.** Curva, graduación, primeros drops.
2. **Dashboard.** Saldo del vault en vivo, cuenta regresiva al siguiente drop, tus nutz pendientes, historial y cuenta regresiva del Acorn Draw.
3. **Nutz Generator.** Generador de PFP y memes (Ardilla del Tornado + captions Kinda Chic) para que los holders publiquen sus drops.
4. **Pool secundario emparejado con acción.** Si la comunidad lo quiere, un pool NUTZ/SPCX o NUTZ/NVDA en Uniswap tras la graduación pone a NUTZ en los screeners de pares con acciones sin condicionar el lanzamiento.
5. **Cambios en la canasta:** ninguno. El Stash es fijo para siempre.

Sin token 2.0. Sin "ecosistema". Sin alianzas anunciadas por la ardilla.

---

## Apéndice A — Lanzamiento cotizado en acción (alternativa)

Pons v2 permite cotizar un lanzamiento en una acción tokenizada en lugar de ETH. Un lanzamiento NUTZ/NVDA o NUTZ/SPCX aparecería bajo el filtro "Stocks" y pagaría las comisiones del creador directamente en ese Stock Token. Se descartó: pone un swap extra frente a cada comprador (menos holders para una moneda cuyo sentido son los holders), un pool exitoso puede terminar con una gran parte del float on-chain de un Stock Token (una memecoin absorbió más de la mitad de HIMS y distorsionó su precio — un imán regulatorio), el objetivo de graduación se mueve con la acción, y hace que NUTZ se lea como "la moneda de X" cuando su historia es una canasta de cuatro acciones. Si la comunidad quiere exposición a pares con acciones más adelante, cualquiera puede abrir un pool secundario NUTZ/acción en Uniswap tras la graduación; está en el roadmap como opción, no como decisión de lanzamiento.

## Apéndice B — Decisiones

| # | Decisión | Valor | Estado |
|---|---|---|---|
| 1 | Launchpad (Pons vs Flap) | Pons v2 | **Decidido** |
| 2 | Activo de cotización (ETH vs acción tokenizada) | ETH | **Decidido** |
| 3 | Reparto (Stash / Cash / Acorn Draw / Ops) | 60 / 28 / 10 / 2 | **Decidido** |
| 3b | Modelo de gas | Parte Ops del 2 % + envío automático pagado por el receptor | **Decidido** |
| 4 | Composición de la canasta | SPY, NVDA, MU, SPCX (liquidez verificada 9 sep) | **Decidido** |
| 5 | Buyback | Apagado | **Decidido** |
| 6 | Duración de época | 1 hora | **Decidido** |
| 6b | Modelo de distribución | Verificable por Merkle desde el día uno | **Decidido** |
| 6c | Multiplicador de la wallet dev | 1.0× fijo, sin Acorn Draw | **Decidido** |
| 7 | Niveles de racha | 7d 1.25× / 30d 1.5× | Eduar |
| 7b | Mínimos y ratios de escalado del Acorn Draw | $2.5k / $250 / $25; ÷1,000 y ÷100 | Eduar |
| 8 | Canasta fija vs gobernable | Fija para siempre | **Decidido** |
| 9 | Momento de la auditoría | Autorevisión + herramientas + bounty al lanzar; auditoría externa post-lanzamiento | **Decidido** |
| 10 | Tratamiento jurisdiccional de las recompensas en Stock Tokens | Pendiente de revisión legal | Legal |

## Apéndice C — Glosario

- **Nut Vault** — el conjunto de contratos de código abierto (Converter, Distributor, sorteo) que recibe el 100 % de las comisiones del creador y paga a los holders.
- **Epoch** — un periodo contable de una hora; las tenencias se miden y las recompensas se asignan por epoch.
- **The Stash** — la canasta fija de acciones tokenizadas (SPY, NVDA, MU, SPCX).
- **The Drop** — la distribución de cada hora.
- **Winter Mode** — el multiplicador de racha por no vender.
- **Acorn Draw** — el sorteo semanal; Golden (1 ganador), Silver y Rain escalan con los holders.
- **TWAB** — saldo promedio ponderado por tiempo; cuánto tuviste y por cuánto tiempo dentro de una época.
- **USDG** — Global Dollar, la stablecoin regulada nativa de Robinhood Chain.
- **Ops** — la parte del 2 %, en ETH, que paga las transacciones del propio sistema.

---

*Borrador v0.2. No es un prospecto, ni una oferta, ni asesoría. Verifica todo on-chain; la dirección del contrato es el único identificador que no se puede falsificar.*
